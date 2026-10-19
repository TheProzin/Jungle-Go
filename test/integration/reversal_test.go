//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	wageringapp "github.com/TheProzin/Jungle-Go/internal/app/wagering"
	walletapp "github.com/TheProzin/Jungle-Go/internal/app/wallet"
	"github.com/TheProzin/Jungle-Go/internal/config"
	domainwagering "github.com/TheProzin/Jungle-Go/internal/domain/wagering"
	"github.com/TheProzin/Jungle-Go/internal/httpapi"
	"github.com/TheProzin/Jungle-Go/internal/infra/postgres"
)

func TestRefundAndRollbackLifecycle(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	walletService := walletapp.NewService(postgres.NewUnitOfWork(pool))
	wageringService := wageringapp.NewService(postgres.NewUnitOfWork(pool))
	playerID := uuid.New()

	wallet := createTestWallet(t, ctx, walletService, playerID, "100.00")

	bet, err := wageringService.ProcessTransaction(ctx, processCommand("provider-a", "bet-1", "idem-bet-1", playerID, wallet.ID(), "round-1", "game-1", domainwagering.KindBet, "10.00", ""))
	if err != nil {
		t.Fatalf("BET error = %v", err)
	}
	assertProcessed(t, bet, "90.00")

	refund, err := wageringService.ProcessTransaction(ctx, processCommand("provider-a", "refund-1", "idem-refund-1", playerID, wallet.ID(), "round-1", "game-1", domainwagering.KindRefund, "10.00", "bet-1"))
	if err != nil {
		t.Fatalf("REFUND error = %v", err)
	}
	assertProcessed(t, refund, "100.00")

	rollbackRefund, err := wageringService.ProcessTransaction(ctx, processCommand("provider-a", "rollback-refund-1", "idem-rollback-refund-1", playerID, wallet.ID(), "round-1", "game-1", domainwagering.KindRollback, "10.00", "refund-1"))
	if err != nil {
		t.Fatalf("ROLLBACK of REFUND error = %v", err)
	}
	assertProcessed(t, rollbackRefund, "90.00")

	win, err := wageringService.ProcessTransaction(ctx, processCommand("provider-a", "win-1", "idem-win-1", playerID, wallet.ID(), "round-1", "game-1", domainwagering.KindWin, "5.00", ""))
	if err != nil {
		t.Fatalf("WIN error = %v", err)
	}
	assertProcessed(t, win, "95.00")

	rollbackWin, err := wageringService.ProcessTransaction(ctx, processCommand("provider-a", "rollback-win-1", "idem-rollback-win-1", playerID, wallet.ID(), "round-1", "game-1", domainwagering.KindRollback, "5.00", "win-1"))
	if err != nil {
		t.Fatalf("ROLLBACK of WIN error = %v", err)
	}
	assertProcessed(t, rollbackWin, "90.00")

	var balanceMinor, version int64
	if err := pool.QueryRow(ctx, `SELECT balance_minor, version FROM wallets WHERE id = $1`, wallet.ID().String()).Scan(&balanceMinor, &version); err != nil {
		t.Fatalf("query wallet: %v", err)
	}
	if balanceMinor != 9000 || version != 6 {
		t.Fatalf("wallet = %d v%d, want 9000 v6", balanceMinor, version)
	}

	assertCount(t, ctx, pool, `SELECT COUNT(*) FROM wallet_ledger_entries`, 6)
	assertCount(t, ctx, pool, `SELECT COUNT(*) FROM wager_transactions WHERE kind = 'REFUND' AND status = 'PROCESSED'`, 1)
	assertCount(t, ctx, pool, `SELECT COUNT(*) FROM wager_transactions WHERE kind = 'ROLLBACK' AND status = 'PROCESSED'`, 2)
}

func TestRefundPendingReferenceResolvedByWorker(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	unitOfWork := postgres.NewUnitOfWork(pool)
	walletService := walletapp.NewService(unitOfWork)
	wageringService := wageringapp.NewServiceWithConfig(unitOfWork, &config.Config{
		ReferenceInitialBackoff: time.Millisecond,
		ReferenceMaxBackoff:     time.Millisecond,
		ReferenceMaxAttempts:    5,
		ReferenceTTL:            time.Hour,
	})
	playerID := uuid.New()

	wallet := createTestWallet(t, ctx, walletService, playerID, "100.00")

	pending, err := wageringService.ProcessTransaction(ctx, processCommand("provider-a", "refund-late", "idem-refund-late", playerID, wallet.ID(), "round-1", "game-1", domainwagering.KindRefund, "10.00", "bet-late"))
	if err != nil {
		t.Fatalf("pending REFUND error = %v", err)
	}
	if pending.Transaction.Status() != domainwagering.StatusPendingReference {
		t.Fatalf("status = %q, want PENDING_REFERENCE", pending.Transaction.Status())
	}
	if pending.Balance != nil {
		t.Fatal("pending reference must not return balance")
	}
	assertCount(t, ctx, pool, `SELECT COUNT(*) FROM outbox_events WHERE event_type = 'WagerTransactionPendingReference'`, 1)

	bet, err := wageringService.ProcessTransaction(ctx, processCommand("provider-a", "bet-late", "idem-bet-late", playerID, wallet.ID(), "round-1", "game-1", domainwagering.KindBet, "10.00", ""))
	if err != nil {
		t.Fatalf("late BET error = %v", err)
	}
	assertProcessed(t, bet, "90.00")

	time.Sleep(3 * time.Millisecond)
	processed, err := wageringService.ProcessNextPendingReference(ctx)
	if err != nil {
		t.Fatalf("ProcessNextPendingReference() error = %v", err)
	}
	if !processed {
		t.Fatal("expected a pending reference to be processed")
	}

	var (
		status        string
		referenceID   *string
		resultBalance *int64
		failureCode   *string
	)
	if err := pool.QueryRow(ctx, `
		SELECT status, reference_transaction_id, result_balance_minor, failure_code
		FROM wager_transactions WHERE id = $1
	`, pending.Transaction.ID().String()).Scan(&status, &referenceID, &resultBalance, &failureCode); err != nil {
		t.Fatalf("query pending transaction: %v", err)
	}
	if status != "PROCESSED" {
		t.Fatalf("status = %q, want PROCESSED", status)
	}
	if referenceID == nil || *referenceID != bet.Transaction.ID().String() {
		t.Fatalf("reference id = %v, want %s", referenceID, bet.Transaction.ID())
	}
	if resultBalance == nil || *resultBalance != 10000 {
		t.Fatalf("result balance = %v, want 10000", resultBalance)
	}
	if failureCode != nil {
		t.Fatalf("failure code = %v, want nil", failureCode)
	}

	var balanceMinor int64
	if err := pool.QueryRow(ctx, `SELECT balance_minor FROM wallets WHERE id = $1`, wallet.ID().String()).Scan(&balanceMinor); err != nil {
		t.Fatalf("query wallet: %v", err)
	}
	if balanceMinor != 10000 {
		t.Fatalf("balance = %d, want 10000", balanceMinor)
	}

	replay, err := wageringService.ProcessTransaction(ctx, processCommand("provider-a", "refund-late", "idem-refund-late", playerID, wallet.ID(), "round-1", "game-1", domainwagering.KindRefund, "10.00", "bet-late"))
	if err != nil {
		t.Fatalf("replay after worker error = %v", err)
	}
	if !replay.IdempotentReplay || replay.Balance == nil || replay.Balance.Amount() != "100.00" {
		t.Fatalf("replay after worker = %+v, want processed balance 100.00", replay)
	}
}

func TestReferenceExpiresAfterMaxAttempts(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	unitOfWork := postgres.NewUnitOfWork(pool)
	walletService := walletapp.NewService(unitOfWork)
	wageringService := wageringapp.NewServiceWithConfig(unitOfWork, &config.Config{
		ReferenceInitialBackoff: time.Millisecond,
		ReferenceMaxBackoff:     time.Millisecond,
		ReferenceMaxAttempts:    2,
		ReferenceTTL:            time.Hour,
	})
	playerID := uuid.New()

	wallet := createTestWallet(t, ctx, walletService, playerID, "100.00")

	pending, err := wageringService.ProcessTransaction(ctx, processCommand("provider-a", "refund-expire", "idem-refund-expire", playerID, wallet.ID(), "round-1", "game-1", domainwagering.KindRefund, "10.00", "missing-bet"))
	if err != nil {
		t.Fatalf("pending REFUND error = %v", err)
	}

	time.Sleep(3 * time.Millisecond)
	processed, err := wageringService.ProcessNextPendingReference(ctx)
	if err != nil {
		t.Fatalf("first worker error = %v", err)
	}
	if !processed {
		t.Fatal("expected first pending attempt")
	}

	var attempts int
	if err := pool.QueryRow(ctx, `SELECT reference_attempts FROM wager_transactions WHERE id = $1`, pending.Transaction.ID().String()).Scan(&attempts); err != nil {
		t.Fatalf("query attempts: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}

	time.Sleep(3 * time.Millisecond)
	processed, err = wageringService.ProcessNextPendingReference(ctx)
	if err != nil {
		t.Fatalf("second worker error = %v", err)
	}
	if !processed {
		t.Fatal("expected second pending attempt")
	}

	var (
		status      string
		failureCode *string
	)
	if err := pool.QueryRow(ctx, `
		SELECT status, failure_code FROM wager_transactions WHERE id = $1
	`, pending.Transaction.ID().String()).Scan(&status, &failureCode); err != nil {
		t.Fatalf("query expired transaction: %v", err)
	}
	if status != "REJECTED" {
		t.Fatalf("status = %q, want REJECTED", status)
	}
	if failureCode == nil || *failureCode != "REFERENCE_NOT_FOUND" {
		t.Fatalf("failure code = %v, want REFERENCE_NOT_FOUND", failureCode)
	}
	assertCount(t, ctx, pool, `SELECT COUNT(*) FROM outbox_events WHERE event_type = 'WagerTransactionRejected'`, 1)
}

func TestDuplicateReversalIsRejected(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	walletService := walletapp.NewService(postgres.NewUnitOfWork(pool))
	wageringService := wageringapp.NewService(postgres.NewUnitOfWork(pool))
	playerID := uuid.New()

	wallet := createTestWallet(t, ctx, walletService, playerID, "100.00")
	if _, err := wageringService.ProcessTransaction(ctx, processCommand("provider-a", "bet-dup", "idem-bet-dup", playerID, wallet.ID(), "round-1", "game-1", domainwagering.KindBet, "10.00", "")); err != nil {
		t.Fatalf("BET error = %v", err)
	}
	if _, err := wageringService.ProcessTransaction(ctx, processCommand("provider-a", "refund-dup-1", "idem-refund-dup-1", playerID, wallet.ID(), "round-1", "game-1", domainwagering.KindRefund, "10.00", "bet-dup")); err != nil {
		t.Fatalf("first REFUND error = %v", err)
	}

	duplicate, err := wageringService.ProcessTransaction(ctx, processCommand("provider-a", "refund-dup-2", "idem-refund-dup-2", playerID, wallet.ID(), "round-1", "game-1", domainwagering.KindRefund, "10.00", "bet-dup"))
	if err != nil {
		t.Fatalf("second REFUND error = %v", err)
	}
	assertRejected(t, duplicate, "DUPLICATE_REVERSAL")

	rollback, err := wageringService.ProcessTransaction(ctx, processCommand("provider-a", "rollback-dup", "idem-rollback-dup", playerID, wallet.ID(), "round-1", "game-1", domainwagering.KindRollback, "10.00", "bet-dup"))
	if err != nil {
		t.Fatalf("ROLLBACK after REFUND error = %v", err)
	}
	assertRejected(t, rollback, "DUPLICATE_REVERSAL")

	var balanceMinor int64
	if err := pool.QueryRow(ctx, `SELECT balance_minor FROM wallets WHERE id = $1`, wallet.ID().String()).Scan(&balanceMinor); err != nil {
		t.Fatalf("query wallet: %v", err)
	}
	if balanceMinor != 10000 {
		t.Fatalf("balance = %d, want 10000", balanceMinor)
	}
	assertCount(t, ctx, pool, `SELECT COUNT(*) FROM wager_transactions WHERE kind IN ('REFUND','ROLLBACK') AND status = 'PROCESSED'`, 1)
}

func TestReferenceMismatchAndAmountMismatch(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	walletService := walletapp.NewService(postgres.NewUnitOfWork(pool))
	wageringService := wageringapp.NewService(postgres.NewUnitOfWork(pool))
	playerID := uuid.New()

	wallet := createTestWallet(t, ctx, walletService, playerID, "100.00")
	if _, err := wageringService.ProcessTransaction(ctx, processCommand("provider-a", "bet-ref-mismatch", "idem-bet-ref-mismatch", playerID, wallet.ID(), "round-1", "game-1", domainwagering.KindBet, "10.00", "")); err != nil {
		t.Fatalf("BET error = %v", err)
	}

	amountMismatch, err := wageringService.ProcessTransaction(ctx, processCommand("provider-a", "refund-amount", "idem-refund-amount", playerID, wallet.ID(), "round-1", "game-1", domainwagering.KindRefund, "11.00", "bet-ref-mismatch"))
	if err != nil {
		t.Fatalf("amount mismatch REFUND error = %v", err)
	}
	assertRejected(t, amountMismatch, "REFERENCE_AMOUNT_MISMATCH")

	roundMismatch, err := wageringService.ProcessTransaction(ctx, processCommand("provider-a", "refund-round", "idem-refund-round", playerID, wallet.ID(), "round-2", "game-1", domainwagering.KindRefund, "10.00", "bet-ref-mismatch"))
	if err != nil {
		t.Fatalf("round mismatch REFUND error = %v", err)
	}
	assertRejected(t, roundMismatch, "REFERENCE_MISMATCH")

	win, err := wageringService.ProcessTransaction(ctx, processCommand("provider-a", "win-ref-mismatch", "idem-win-ref-mismatch", playerID, wallet.ID(), "round-1", "game-1", domainwagering.KindWin, "5.00", ""))
	if err != nil {
		t.Fatalf("WIN error = %v", err)
	}
	assertProcessed(t, win, "95.00")

	invalidKind, err := wageringService.ProcessTransaction(ctx, processCommand("provider-a", "refund-win", "idem-refund-win", playerID, wallet.ID(), "round-1", "game-1", domainwagering.KindRefund, "5.00", "win-ref-mismatch"))
	if err != nil {
		t.Fatalf("REFUND of WIN error = %v", err)
	}
	assertRejected(t, invalidKind, "REFERENCE_MISMATCH")

	var balanceMinor int64
	if err := pool.QueryRow(ctx, `SELECT balance_minor FROM wallets WHERE id = $1`, wallet.ID().String()).Scan(&balanceMinor); err != nil {
		t.Fatalf("query wallet: %v", err)
	}
	if balanceMinor != 9500 {
		t.Fatalf("balance = %d, want 9500", balanceMinor)
	}
}

func TestRollbackInsufficientFundsUsesDistinctFailureCode(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	walletService := walletapp.NewService(postgres.NewUnitOfWork(pool))
	wageringService := wageringapp.NewService(postgres.NewUnitOfWork(pool))
	playerID := uuid.New()

	wallet := createTestWallet(t, ctx, walletService, playerID, "100.00")
	if _, err := wageringService.ProcessTransaction(ctx, processCommand("provider-a", "win-rollback-insufficient", "idem-win-rollback-insufficient", playerID, wallet.ID(), "round-1", "game-1", domainwagering.KindWin, "100.00", "")); err != nil {
		t.Fatalf("WIN error = %v", err)
	}
	if _, err := wageringService.ProcessTransaction(ctx, processCommand("provider-a", "bet-rollback-insufficient", "idem-bet-rollback-insufficient", playerID, wallet.ID(), "round-1", "game-1", domainwagering.KindBet, "150.00", "")); err != nil {
		t.Fatalf("BET error = %v", err)
	}

	rollback, err := wageringService.ProcessTransaction(ctx, processCommand("provider-a", "rollback-insufficient", "idem-rollback-insufficient", playerID, wallet.ID(), "round-1", "game-1", domainwagering.KindRollback, "100.00", "win-rollback-insufficient"))
	if err != nil {
		t.Fatalf("ROLLBACK error = %v", err)
	}
	assertRejected(t, rollback, "INSUFFICIENT_FUNDS_FOR_REVERSAL")

	var balanceMinor int64
	if err := pool.QueryRow(ctx, `SELECT balance_minor FROM wallets WHERE id = $1`, wallet.ID().String()).Scan(&balanceMinor); err != nil {
		t.Fatalf("query wallet: %v", err)
	}
	if balanceMinor != 5000 {
		t.Fatalf("balance = %d, want 5000", balanceMinor)
	}
}

func TestReversalHTTPContract(t *testing.T) {
	pool := newTestPool(t)
	unitOfWork := postgres.NewUnitOfWork(pool)
	walletService := walletapp.NewService(unitOfWork)
	wageringService := wageringapp.NewService(unitOfWork)
	server := newWalletWageringServer(t, pool, walletService, wageringService)
	defer server.Close()
	client := server.Client()

	playerID := uuid.New()
	createResponse, createBody := doJSON(t, client, http.MethodPost, server.URL+"/wallets", nil, map[string]any{
		"playerId":       playerID.String(),
		"initialBalance": map[string]string{"amount": "100.00", "currency": "BRL"},
	})
	if createResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create wallet status = %d, body = %s", createResponse.StatusCode, createBody)
	}
	var wallet struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(createBody, &wallet); err != nil {
		t.Fatalf("decode wallet: %v", err)
	}

	pendingResponse, pendingBody := doJSON(t, client, http.MethodPost, server.URL+"/wagering/transactions", map[string]string{"Idempotency-Key": "provider-a:refund-pending"}, processRequestBody("provider-a", "refund-pending", playerID.String(), wallet.ID, "round-1", "game-1", "REFUND", "10.00", "bet-not-there"))
	if pendingResponse.StatusCode != http.StatusAccepted {
		t.Fatalf("pending status = %d, body = %s", pendingResponse.StatusCode, pendingBody)
	}
	var pending struct {
		TransactionID string `json:"transactionId"`
		Status        string `json:"status"`
	}
	if err := json.Unmarshal(pendingBody, &pending); err != nil {
		t.Fatalf("decode pending: %v", err)
	}
	if pending.Status != "PENDING_REFERENCE" {
		t.Fatalf("status = %q, want PENDING_REFERENCE", pending.Status)
	}

	replayResponse, replayBody := doJSON(t, client, http.MethodPost, server.URL+"/wagering/transactions", map[string]string{"Idempotency-Key": "provider-a:refund-pending"}, processRequestBody("provider-a", "refund-pending", playerID.String(), wallet.ID, "round-1", "game-1", "REFUND", "10.00", "bet-not-there"))
	if replayResponse.StatusCode != http.StatusAccepted {
		t.Fatalf("pending replay status = %d, body = %s", replayResponse.StatusCode, replayBody)
	}
	var replay struct {
		IdempotentReplay bool `json:"idempotentReplay"`
	}
	if err := json.Unmarshal(replayBody, &replay); err != nil {
		t.Fatalf("decode pending replay: %v", err)
	}
	if !replay.IdempotentReplay {
		t.Fatal("expected idempotentReplay=true")
	}

	getResponse, getBody := doJSON(t, client, http.MethodGet, server.URL+"/wagering/transactions/"+pending.TransactionID, nil, nil)
	if getResponse.StatusCode != http.StatusOK {
		t.Fatalf("get pending status = %d, body = %s", getResponse.StatusCode, getBody)
	}
	var fetched struct {
		Status            string     `json:"status"`
		ReferenceAttempts int        `json:"referenceAttempts"`
		NextReferenceAt   *time.Time `json:"nextReferenceAt"`
	}
	if err := json.Unmarshal(getBody, &fetched); err != nil {
		t.Fatalf("decode pending transaction: %v", err)
	}
	if fetched.Status != "PENDING_REFERENCE" || fetched.ReferenceAttempts < 1 || fetched.NextReferenceAt == nil {
		t.Fatalf("unexpected pending transaction: %+v", fetched)
	}

	missingReferenceResponse, _ := doJSON(t, client, http.MethodPost, server.URL+"/wagering/transactions", map[string]string{"Idempotency-Key": "provider-a:refund-no-reference"}, processRequestBody("provider-a", "refund-no-reference", playerID.String(), wallet.ID, "round-1", "game-1", "REFUND", "10.00", ""))
	if missingReferenceResponse.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing reference status = %d, want 400", missingReferenceResponse.StatusCode)
	}
}

func newWalletWageringServer(t *testing.T, pool *pgxpool.Pool, walletService *walletapp.Service, wageringService *wageringapp.Service) *httptest.Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	health := httpapi.NewHealth(pool, nil, &config.Config{ReadinessTimeout: time.Second}, logger)
	return httptest.NewServer(httpapi.NewMux(
		health,
		httpapi.NewWalletHandler(walletService),
		httpapi.NewWageringHandler(wageringService),
	))
}
