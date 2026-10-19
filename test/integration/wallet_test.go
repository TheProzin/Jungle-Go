//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	walletapp "github.com/TheProzin/Jungle-Go/internal/app/wallet"
	"github.com/TheProzin/Jungle-Go/internal/config"
	"github.com/TheProzin/Jungle-Go/internal/domain/money"
	domainwallet "github.com/TheProzin/Jungle-Go/internal/domain/wallet"
	"github.com/TheProzin/Jungle-Go/internal/httpapi"
	"github.com/TheProzin/Jungle-Go/internal/infra/postgres"
)

func TestCreateWalletWithOpeningLedgerAndOutbox(t *testing.T) {
	pool := newTestPool(t)
	service := walletapp.NewService(postgres.NewUnitOfWork(pool))
	ctx := context.Background()
	playerID := uuid.New()
	initial, _ := money.Parse("1000.00", "BRL")

	created, err := service.CreateWallet(ctx, walletapp.CreateWalletCommand{
		PlayerID:       playerID,
		InitialBalance: initial,
		CorrelationID:  "correlation-1",
	})
	if err != nil {
		t.Fatalf("CreateWallet() error = %v", err)
	}
	if created.Balance().Amount() != "1000.00" || created.Version() != 1 {
		t.Fatalf("created wallet = %s v%d, want 1000.00 v1", created.Balance().Amount(), created.Version())
	}

	var (
		walletBalance int64
		walletVersion int64
	)
	if err := pool.QueryRow(ctx, `
		SELECT balance_minor, version FROM wallets WHERE id = $1
	`, created.ID().String()).Scan(&walletBalance, &walletVersion); err != nil {
		t.Fatalf("query wallet: %v", err)
	}
	if walletBalance != 100000 || walletVersion != 1 {
		t.Fatalf("stored wallet = %d v%d, want 100000 v1", walletBalance, walletVersion)
	}

	var (
		kind   string
		status string
	)
	if err := pool.QueryRow(ctx, `
		SELECT kind, status FROM wager_transactions WHERE wallet_id = $1
	`, created.ID().String()).Scan(&kind, &status); err != nil {
		t.Fatalf("query opening: %v", err)
	}
	if kind != "OPENING" || status != "PROCESSED" {
		t.Fatalf("opening = %s/%s, want OPENING/PROCESSED", kind, status)
	}

	var (
		direction string
		amount    int64
		before    int64
		after     int64
	)
	if err := pool.QueryRow(ctx, `
		SELECT direction, amount_minor, balance_before_minor, balance_after_minor
		FROM wallet_ledger_entries WHERE wallet_id = $1
	`, created.ID().String()).Scan(&direction, &amount, &before, &after); err != nil {
		t.Fatalf("query ledger: %v", err)
	}
	if direction != "CREDIT" || amount != 100000 || before != 0 || after != 100000 {
		t.Fatalf("ledger = %s %d %d %d, want CREDIT 100000 0 100000", direction, amount, before, after)
	}

	var processedEvents, balanceEvents int
	if err := pool.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE event_type = 'WagerTransactionProcessed'),
			COUNT(*) FILTER (WHERE event_type = 'WalletBalanceChanged')
		FROM outbox_events
		WHERE aggregate_id = $1 OR aggregate_id = $2
	`, created.ID().String(), openingIDForWallet(t, ctx, pool, created.ID())).Scan(&processedEvents, &balanceEvents); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if processedEvents != 1 || balanceEvents != 1 {
		t.Fatalf("outbox events = processed:%d balance:%d, want 1 and 1", processedEvents, balanceEvents)
	}

	reconciliation, err := service.Reconcile(ctx, created.ID())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if !reconciliation.Consistent || reconciliation.CheckedEntries != 1 {
		t.Fatalf("reconciliation = consistent:%t entries:%d, want true and 1", reconciliation.Consistent, reconciliation.CheckedEntries)
	}
	if reconciliation.Difference.Amount() != "0.00" {
		t.Fatalf("difference = %s, want 0.00", reconciliation.Difference.Amount())
	}
}

func TestCreateWalletWithZeroBalanceCreatesNoFinancialRecords(t *testing.T) {
	pool := newTestPool(t)
	service := walletapp.NewService(postgres.NewUnitOfWork(pool))
	ctx := context.Background()
	zero, _ := money.Zero("BRL")

	created, err := service.CreateWallet(ctx, walletapp.CreateWalletCommand{
		PlayerID:       uuid.New(),
		InitialBalance: zero,
		CorrelationID:  "correlation-zero",
	})
	if err != nil {
		t.Fatalf("CreateWallet() error = %v", err)
	}
	if created.Balance().Amount() != "0.00" {
		t.Fatalf("balance = %s, want 0.00", created.Balance().Amount())
	}

	assertCount(t, ctx, pool, `SELECT COUNT(*) FROM wager_transactions`, 0)
	assertCount(t, ctx, pool, `SELECT COUNT(*) FROM wallet_ledger_entries`, 0)
	assertCount(t, ctx, pool, `SELECT COUNT(*) FROM outbox_events`, 0)
}

func TestCreateWalletRejectsDuplicatePlayerAndCurrency(t *testing.T) {
	pool := newTestPool(t)
	service := walletapp.NewService(postgres.NewUnitOfWork(pool))
	ctx := context.Background()
	playerID := uuid.New()
	initial, _ := money.Parse("10.00", "BRL")

	_, err := service.CreateWallet(ctx, walletapp.CreateWalletCommand{PlayerID: playerID, InitialBalance: initial})
	if err != nil {
		t.Fatalf("first CreateWallet() error = %v", err)
	}

	_, err = service.CreateWallet(ctx, walletapp.CreateWalletCommand{PlayerID: playerID, InitialBalance: initial})
	if !errors.Is(err, domainwallet.ErrWalletAlreadyExists) {
		t.Fatalf("second CreateWallet() error = %v, want ErrWalletAlreadyExists", err)
	}
	assertCount(t, ctx, pool, `SELECT COUNT(*) FROM wager_transactions`, 1)
	assertCount(t, ctx, pool, `SELECT COUNT(*) FROM wallet_ledger_entries`, 1)
}

func TestLedgerPaginationAndReconciliation(t *testing.T) {
	pool := newTestPool(t)
	service := walletapp.NewService(postgres.NewUnitOfWork(pool))
	ctx := context.Background()
	playerID := uuid.New()
	initial, _ := money.Parse("1000.00", "BRL")

	created, err := service.CreateWallet(ctx, walletapp.CreateWalletCommand{
		PlayerID:       playerID,
		InitialBalance: initial,
		CorrelationID:  "correlation-pagination",
	})
	if err != nil {
		t.Fatalf("CreateWallet() error = %v", err)
	}

	betID := uuid.New()
	betExternalID := "bet-pagination"
	mustExec(t, ctx, pool, `
		INSERT INTO wager_transactions (
			id, origin, kind, status, provider_id, external_transaction_id, idempotency_key,
			payload_hash, wallet_id, player_id, round_id, game_id, amount_minor, currency,
			result_balance_minor, result_currency, created_at, updated_at, processed_at
		) VALUES ($1, 'EXTERNAL', 'BET', 'PROCESSED', 'provider-a', $2, $3, 'hash', $4, $5,
			'round-1', 'game-1', 10000, 'BRL', 90000, 'BRL', now() + interval '1 second',
			now() + interval '1 second', now() + interval '1 second')
	`, betID.String(), betExternalID, "idem-pagination", created.ID().String(), playerID.String())

	mustExec(t, ctx, pool, `
		INSERT INTO wallet_ledger_entries (
			id, wallet_id, transaction_id, direction, amount_minor, currency,
			balance_before_minor, balance_after_minor, created_at
		) VALUES ($1, $2, $3, 'DEBIT', 10000, 'BRL', 100000, 90000, now() + interval '1 second')
	`, uuid.NewString(), created.ID().String(), betID.String())

	mustExec(t, ctx, pool, `
		UPDATE wallets SET balance_minor = 90000, version = 2, updated_at = now() + interval '1 second'
		WHERE id = $1
	`, created.ID().String())

	first, err := service.GetLedger(ctx, created.ID(), "", 1)
	if err != nil {
		t.Fatalf("GetLedger(first) error = %v", err)
	}
	if len(first.Entries) != 1 || first.NextCursor == "" {
		t.Fatalf("first page = %d entries next=%q, want 1 and non-empty", len(first.Entries), first.NextCursor)
	}

	second, err := service.GetLedger(ctx, created.ID(), first.NextCursor, 1)
	if err != nil {
		t.Fatalf("GetLedger(second) error = %v", err)
	}
	if len(second.Entries) != 1 || second.NextCursor != "" {
		t.Fatalf("second page = %d entries next=%q, want 1 and empty", len(second.Entries), second.NextCursor)
	}

	reconciliation, err := service.Reconcile(ctx, created.ID())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if !reconciliation.Consistent || reconciliation.CheckedEntries != 2 {
		t.Fatalf("reconciliation = consistent:%t entries:%d, want true and 2", reconciliation.Consistent, reconciliation.CheckedEntries)
	}
	if reconciliation.CalculatedBalance.Amount() != "900.00" {
		t.Fatalf("calculated balance = %s, want 900.00", reconciliation.CalculatedBalance.Amount())
	}
}

func TestWalletHTTPContract(t *testing.T) {
	pool := newTestPool(t)
	service := walletapp.NewService(postgres.NewUnitOfWork(pool))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := httpapi.NewWalletHandler(service)
	health := httpapi.NewHealth(pool, nil, &config.Config{ReadinessTimeout: time.Second}, logger)
	server := httptest.NewServer(httpapi.NewMux(health, handler))
	defer server.Close()

	playerID := uuid.New()
	body := `{"playerId":"` + playerID.String() + `","initialBalance":{"amount":"1000.00","currency":"BRL"}}`

	response, err := http.Post(server.URL+"/wallets", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST /wallets error = %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("POST /wallets status = %d, want 201", response.StatusCode)
	}

	var created struct {
		ID       string      `json:"id"`
		PlayerID string      `json:"playerId"`
		Balance  money.Money `json:"balance"`
		Version  int64       `json:"version"`
	}
	if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.ID == "" || created.PlayerID != playerID.String() || created.Balance.Amount() != "1000.00" || created.Version != 1 {
		t.Fatalf("unexpected create response: %+v", created)
	}

	getResponse, err := http.Get(server.URL + "/wallets/" + created.ID)
	if err != nil {
		t.Fatalf("GET /wallets error = %v", err)
	}
	defer getResponse.Body.Close()
	if getResponse.StatusCode != http.StatusOK {
		t.Fatalf("GET /wallets status = %d, want 200", getResponse.StatusCode)
	}

	ledgerResponse, err := http.Get(server.URL + "/wallets/" + created.ID + "/ledger?limit=50")
	if err != nil {
		t.Fatalf("GET ledger error = %v", err)
	}
	defer ledgerResponse.Body.Close()
	if ledgerResponse.StatusCode != http.StatusOK {
		t.Fatalf("GET ledger status = %d, want 200", ledgerResponse.StatusCode)
	}
	var page struct {
		Items []struct {
			Direction string      `json:"direction"`
			Money     money.Money `json:"money"`
		} `json:"items"`
	}
	if err := json.NewDecoder(ledgerResponse.Body).Decode(&page); err != nil {
		t.Fatalf("decode ledger response: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].Direction != "CREDIT" || page.Items[0].Money.Amount() != "1000.00" {
		t.Fatalf("unexpected ledger response: %+v", page)
	}

	reconciliationResponse, err := http.Post(server.URL+"/wallets/"+created.ID+"/reconciliation", "application/json", nil)
	if err != nil {
		t.Fatalf("POST reconciliation error = %v", err)
	}
	defer reconciliationResponse.Body.Close()
	if reconciliationResponse.StatusCode != http.StatusOK {
		t.Fatalf("POST reconciliation status = %d, want 200", reconciliationResponse.StatusCode)
	}
	var reconciliation struct {
		Consistent     bool        `json:"consistent"`
		CheckedEntries int         `json:"checkedEntries"`
		Difference     money.Money `json:"difference"`
	}
	if err := json.NewDecoder(reconciliationResponse.Body).Decode(&reconciliation); err != nil {
		t.Fatalf("decode reconciliation response: %v", err)
	}
	if !reconciliation.Consistent || reconciliation.CheckedEntries != 1 || reconciliation.Difference.Amount() != "0.00" {
		t.Fatalf("unexpected reconciliation response: %+v", reconciliation)
	}

	duplicate, err := http.Post(server.URL+"/wallets", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("duplicate POST error = %v", err)
	}
	defer duplicate.Body.Close()
	if duplicate.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate status = %d, want 409", duplicate.StatusCode)
	}
	var apiError struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(duplicate.Body).Decode(&apiError); err != nil {
		t.Fatalf("decode duplicate response: %v", err)
	}
	if apiError.Error.Code != "WALLET_ALREADY_EXISTS" {
		t.Fatalf("duplicate code = %q, want WALLET_ALREADY_EXISTS", apiError.Error.Code)
	}
}

func openingIDForWallet(t *testing.T, ctx context.Context, pool *pgxpool.Pool, walletID uuid.UUID) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(ctx, `SELECT id FROM wager_transactions WHERE wallet_id = $1 AND kind = 'OPENING'`, walletID.String()).Scan(&id); err != nil {
		t.Fatalf("query opening id: %v", err)
	}
	return id
}

func assertCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, query string, want int) {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, query).Scan(&count); err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != want {
		t.Fatalf("count = %d, want %d", count, want)
	}
}
