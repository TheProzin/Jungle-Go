//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	wageringapp "github.com/TheProzin/Jungle-Go/internal/app/wagering"
	walletapp "github.com/TheProzin/Jungle-Go/internal/app/wallet"
	"github.com/TheProzin/Jungle-Go/internal/domain/money"
	domainwagering "github.com/TheProzin/Jungle-Go/internal/domain/wagering"
	domainwallet "github.com/TheProzin/Jungle-Go/internal/domain/wallet"
	"github.com/TheProzin/Jungle-Go/internal/infra/postgres"
)

func TestProcessBetWinAndLoss(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	walletService := walletapp.NewService(postgres.NewUnitOfWork(pool))
	wageringService := wageringapp.NewService(postgres.NewUnitOfWork(pool))
	playerID := uuid.New()

	wallet := createTestWallet(t, ctx, walletService, playerID, "100.00")

	bet, err := wageringService.ProcessTransaction(ctx, processCommand("provider-a", "bet-1", "idem-bet-1", playerID, wallet.ID(), "round-1", "game-1", domainwagering.KindBet, "25.00", ""))
	if err != nil {
		t.Fatalf("BET error = %v", err)
	}
	assertProcessed(t, bet, "75.00")

	win, err := wageringService.ProcessTransaction(ctx, processCommand("provider-a", "win-1", "idem-win-1", playerID, wallet.ID(), "round-1", "game-1", domainwagering.KindWin, "10.00", ""))
	if err != nil {
		t.Fatalf("WIN error = %v", err)
	}
	assertProcessed(t, win, "85.00")

	loss, err := wageringService.ProcessTransaction(ctx, processCommand("provider-a", "loss-1", "idem-loss-1", playerID, wallet.ID(), "round-1", "game-1", domainwagering.KindLoss, "0.00", ""))
	if err != nil {
		t.Fatalf("LOSS error = %v", err)
	}
	assertProcessed(t, loss, "85.00")

	var balanceMinor, version int64
	if err := pool.QueryRow(ctx, `SELECT balance_minor, version FROM wallets WHERE id = $1`, wallet.ID().String()).Scan(&balanceMinor, &version); err != nil {
		t.Fatalf("query wallet: %v", err)
	}
	if balanceMinor != 8500 || version != 3 {
		t.Fatalf("wallet = %d v%d, want 8500 v3", balanceMinor, version)
	}

	assertCount(t, ctx, pool, `SELECT COUNT(*) FROM wallet_ledger_entries`, 3)
	assertCount(t, ctx, pool, `SELECT COUNT(*) FROM wallet_ledger_entries WHERE transaction_id = '`+loss.Transaction.ID().String()+`'`, 0)
	assertCount(t, ctx, pool, `SELECT COUNT(*) FROM wager_transactions`, 4)
	assertCount(t, ctx, pool, `SELECT COUNT(*) FROM outbox_events WHERE event_type = 'WagerTransactionProcessed'`, 4)
	assertCount(t, ctx, pool, `SELECT COUNT(*) FROM outbox_events WHERE event_type = 'WalletBalanceChanged'`, 3)
}

func TestInsufficientFundsIsPersistedAsRejection(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	walletService := walletapp.NewService(postgres.NewUnitOfWork(pool))
	wageringService := wageringapp.NewService(postgres.NewUnitOfWork(pool))
	playerID := uuid.New()

	wallet := createTestWallet(t, ctx, walletService, playerID, "10.00")
	command := processCommand("provider-a", "bet-insufficient", "idem-insufficient", playerID, wallet.ID(), "round-1", "game-1", domainwagering.KindBet, "20.00", "")

	result, err := wageringService.ProcessTransaction(ctx, command)
	if err != nil {
		t.Fatalf("ProcessTransaction() error = %v", err)
	}
	if result.Transaction.Status() != domainwagering.StatusRejected {
		t.Fatalf("status = %q, want REJECTED", result.Transaction.Status())
	}
	if result.Transaction.FailureCode() == nil || *result.Transaction.FailureCode() != "INSUFFICIENT_FUNDS" {
		t.Fatalf("failure code = %v, want INSUFFICIENT_FUNDS", result.Transaction.FailureCode())
	}
	if result.Balance != nil {
		t.Fatal("rejection must not return balance")
	}

	var balanceMinor, version int64
	if err := pool.QueryRow(ctx, `SELECT balance_minor, version FROM wallets WHERE id = $1`, wallet.ID().String()).Scan(&balanceMinor, &version); err != nil {
		t.Fatalf("query wallet: %v", err)
	}
	if balanceMinor != 1000 || version != 1 {
		t.Fatalf("wallet = %d v%d, want 1000 v1", balanceMinor, version)
	}

	assertCount(t, ctx, pool, `SELECT COUNT(*) FROM wager_transactions`, 2)
	assertCount(t, ctx, pool, `SELECT COUNT(*) FROM wallet_ledger_entries`, 1)
	assertCount(t, ctx, pool, `SELECT COUNT(*) FROM outbox_events WHERE event_type = 'WagerTransactionRejected'`, 1)

	replay, err := wageringService.ProcessTransaction(ctx, command)
	if err != nil {
		t.Fatalf("replay error = %v", err)
	}
	if !replay.IdempotentReplay || replay.Transaction.ID() != result.Transaction.ID() {
		t.Fatalf("replay = %+v, want same rejected transaction", replay)
	}
	assertCount(t, ctx, pool, `SELECT COUNT(*) FROM wager_transactions`, 2)
}

func TestIdempotencyReplayAndConflicts(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	walletService := walletapp.NewService(postgres.NewUnitOfWork(pool))
	wageringService := wageringapp.NewService(postgres.NewUnitOfWork(pool))
	playerID := uuid.New()

	wallet := createTestWallet(t, ctx, walletService, playerID, "100.00")
	firstCommand := processCommand("provider-a", "bet-idem", "idem-key-1", playerID, wallet.ID(), "round-1", "game-1", domainwagering.KindBet, "10.00", "")

	first, err := wageringService.ProcessTransaction(ctx, firstCommand)
	if err != nil {
		t.Fatalf("first BET error = %v", err)
	}
	assertProcessed(t, first, "90.00")

	replay, err := wageringService.ProcessTransaction(ctx, firstCommand)
	if err != nil {
		t.Fatalf("replay error = %v", err)
	}
	if !replay.IdempotentReplay || replay.Transaction.ID() != first.Transaction.ID() {
		t.Fatalf("replay = %+v, want same transaction", replay)
	}
	if replay.Balance == nil || replay.Balance.Amount() != "90.00" {
		t.Fatalf("replay balance = %v, want 90.00", replay.Balance)
	}

	second, err := wageringService.ProcessTransaction(ctx, processCommand("provider-a", "bet-idem-2", "idem-key-2", playerID, wallet.ID(), "round-1", "game-1", domainwagering.KindBet, "5.00", ""))
	if err != nil {
		t.Fatalf("second BET error = %v", err)
	}
	assertProcessed(t, second, "85.00")

	originalReplay, err := wageringService.ProcessTransaction(ctx, firstCommand)
	if err != nil {
		t.Fatalf("original replay error = %v", err)
	}
	if originalReplay.Balance == nil || originalReplay.Balance.Amount() != "90.00" {
		t.Fatalf("original replay balance = %v, want 90.00", originalReplay.Balance)
	}

	conflicting := firstCommand
	conflicting.Amount, _ = money.Parse("11.00", "BRL")
	if _, err := wageringService.ProcessTransaction(ctx, conflicting); !errors.Is(err, domainwagering.ErrIdempotencyKeyConflict) {
		t.Fatalf("conflicting key error = %v, want ErrIdempotencyKeyConflict", err)
	}

	differentKey := firstCommand
	differentKey.IdempotencyKey = "idem-key-3"
	if _, err := wageringService.ProcessTransaction(ctx, differentKey); !errors.Is(err, domainwagering.ErrExternalTransactionConflict) {
		t.Fatalf("different key error = %v, want ErrExternalTransactionConflict", err)
	}

	assertCount(t, ctx, pool, `SELECT COUNT(*) FROM wager_transactions WHERE external_transaction_id = 'bet-idem'`, 1)
	assertCount(t, ctx, pool, `SELECT COUNT(*) FROM wallet_ledger_entries WHERE transaction_id = '`+first.Transaction.ID().String()+`'`, 1)

	var balanceMinor int64
	if err := pool.QueryRow(ctx, `SELECT balance_minor FROM wallets WHERE id = $1`, wallet.ID().String()).Scan(&balanceMinor); err != nil {
		t.Fatalf("query wallet: %v", err)
	}
	if balanceMinor != 8500 {
		t.Fatalf("balance = %d, want 8500", balanceMinor)
	}
}

func TestConcurrentDuplicateBetProcessesOnce(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	walletService := walletapp.NewService(postgres.NewUnitOfWork(pool))
	wageringService := wageringapp.NewService(postgres.NewUnitOfWork(pool))
	playerID := uuid.New()

	wallet := createTestWallet(t, ctx, walletService, playerID, "100.00")
	command := processCommand("provider-a", "bet-concurrent", "idem-concurrent", playerID, wallet.ID(), "round-1", "game-1", domainwagering.KindBet, "10.00", "")

	const workers = 50
	var waitGroup sync.WaitGroup
	errorsChannel := make(chan error, workers)
	replays := make(chan bool, workers)

	for i := 0; i < workers; i++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			result, err := wageringService.ProcessTransaction(ctx, command)
			if err != nil {
				errorsChannel <- err
				return
			}
			replays <- result.IdempotentReplay
		}()
	}
	waitGroup.Wait()
	close(errorsChannel)
	close(replays)

	for err := range errorsChannel {
		t.Fatalf("concurrent error = %v", err)
	}

	nonReplays := 0
	for replay := range replays {
		if !replay {
			nonReplays++
		}
	}
	if nonReplays != 1 {
		t.Fatalf("non-replays = %d, want 1", nonReplays)
	}

	assertCount(t, ctx, pool, `SELECT COUNT(*) FROM wager_transactions WHERE external_transaction_id = 'bet-concurrent'`, 1)
	assertCount(t, ctx, pool, `SELECT COUNT(*) FROM wallet_ledger_entries WHERE transaction_id IN (SELECT id FROM wager_transactions WHERE external_transaction_id = 'bet-concurrent')`, 1)

	var balanceMinor, version int64
	if err := pool.QueryRow(ctx, `SELECT balance_minor, version FROM wallets WHERE id = $1`, wallet.ID().String()).Scan(&balanceMinor, &version); err != nil {
		t.Fatalf("query wallet: %v", err)
	}
	if balanceMinor != 9000 || version != 2 {
		t.Fatalf("wallet = %d v%d, want 9000 v2", balanceMinor, version)
	}
}

func TestTwoDistinctBetsOverSharedBalance(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	walletService := walletapp.NewService(postgres.NewUnitOfWork(pool))
	wageringService := wageringapp.NewService(postgres.NewUnitOfWork(pool))
	playerID := uuid.New()

	wallet := createTestWallet(t, ctx, walletService, playerID, "100.00")
	commands := []wageringapp.ProcessCommand{
		processCommand("provider-a", "bet-80-a", "idem-80-a", playerID, wallet.ID(), "round-1", "game-1", domainwagering.KindBet, "80.00", ""),
		processCommand("provider-a", "bet-80-b", "idem-80-b", playerID, wallet.ID(), "round-1", "game-1", domainwagering.KindBet, "80.00", ""),
	}

	type outcome struct {
		result *wageringapp.ProcessResult
		err    error
	}
	outcomes := make(chan outcome, len(commands))
	var waitGroup sync.WaitGroup

	for _, command := range commands {
		waitGroup.Add(1)
		go func(cmd wageringapp.ProcessCommand) {
			defer waitGroup.Done()
			result, err := wageringService.ProcessTransaction(ctx, cmd)
			outcomes <- outcome{result: result, err: err}
		}(command)
	}
	waitGroup.Wait()
	close(outcomes)

	processed := 0
	rejected := 0
	for outcome := range outcomes {
		if outcome.err != nil {
			t.Fatalf("concurrent BET error = %v", outcome.err)
		}
		switch outcome.result.Transaction.Status() {
		case domainwagering.StatusProcessed:
			processed++
		case domainwagering.StatusRejected:
			rejected++
			assertRejected(t, outcome.result, "INSUFFICIENT_FUNDS")
		default:
			t.Fatalf("unexpected status = %q", outcome.result.Transaction.Status())
		}
	}

	if processed != 1 || rejected != 1 {
		t.Fatalf("processed = %d, rejected = %d; want 1 and 1", processed, rejected)
	}

	var balanceMinor, version int64
	if err := pool.QueryRow(ctx, `SELECT balance_minor, version FROM wallets WHERE id = $1`, wallet.ID().String()).Scan(&balanceMinor, &version); err != nil {
		t.Fatalf("query wallet: %v", err)
	}
	if balanceMinor != 2000 || version != 2 {
		t.Fatalf("wallet = %d v%d, want 2000 v2", balanceMinor, version)
	}

	assertCount(t, ctx, pool, `SELECT COUNT(*) FROM wallet_ledger_entries WHERE direction = 'DEBIT'`, 1)
	assertCount(t, ctx, pool, `SELECT COUNT(*) FROM wager_transactions WHERE kind = 'BET' AND status = 'PROCESSED'`, 1)
	assertCount(t, ctx, pool, `SELECT COUNT(*) FROM wager_transactions WHERE kind = 'BET' AND status = 'REJECTED'`, 1)
}

func TestParallelIndependentWallets(t *testing.T) {
	const walletCount = 5

	pool := newTestPool(t)
	ctx := context.Background()
	walletService := walletapp.NewService(postgres.NewUnitOfWork(pool))
	wageringService := wageringapp.NewService(postgres.NewUnitOfWork(pool))

	wallets := make([]*domainwallet.Wallet, walletCount)
	for index := range wallets {
		wallets[index] = createTestWallet(t, ctx, walletService, uuid.New(), "100.00")
	}

	errs := make([]error, walletCount)
	var waitGroup sync.WaitGroup

	for index, wallet := range wallets {
		waitGroup.Add(1)
		go func(index int, wallet *domainwallet.Wallet) {
			defer waitGroup.Done()
			_, errs[index] = wageringService.ProcessTransaction(ctx, processCommand(
				"provider-a",
				fmt.Sprintf("bet-parallel-%d", index),
				fmt.Sprintf("idem-parallel-%d", index),
				wallet.PlayerID(),
				wallet.ID(),
				"round-1",
				"game-1",
				domainwagering.KindBet,
				"10.00",
				"",
			))
		}(index, wallet)
	}
	waitGroup.Wait()

	for index, err := range errs {
		if err != nil {
			t.Fatalf("wallet %d error = %v", index, err)
		}
	}

	for index, wallet := range wallets {
		var balanceMinor int64
		if err := pool.QueryRow(ctx, `SELECT balance_minor FROM wallets WHERE id = $1`, wallet.ID().String()).Scan(&balanceMinor); err != nil {
			t.Fatalf("wallet %d balance: %v", index, err)
		}
		if balanceMinor != 9000 {
			t.Fatalf("wallet %d balance = %d, want 9000", index, balanceMinor)
		}
	}
}

func TestThreeIndependentServices(t *testing.T) {
	pool1, dsn := newTestDatabase(t, true)
	ctx := context.Background()

	pool2, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("create second pool: %v", err)
	}
	pool3, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("create third pool: %v", err)
	}
	t.Cleanup(func() {
		pool2.Close()
		pool3.Close()
	})

	walletService := walletapp.NewService(postgres.NewUnitOfWork(pool1))
	wallet := createTestWallet(t, ctx, walletService, uuid.New(), "100.00")

	services := []*wageringapp.Service{
		wageringapp.NewService(postgres.NewUnitOfWork(pool1)),
		wageringapp.NewService(postgres.NewUnitOfWork(pool2)),
		wageringapp.NewService(postgres.NewUnitOfWork(pool3)),
	}
	command := processCommand("provider-a", "bet-three-instances", "idem-three-instances", wallet.PlayerID(), wallet.ID(), "round-1", "game-1", domainwagering.KindBet, "10.00", "")

	const workers = 50
	var waitGroup sync.WaitGroup
	errorsChannel := make(chan error, workers)
	replays := make(chan bool, workers)

	for index := 0; index < workers; index++ {
		waitGroup.Add(1)
		go func(index int) {
			defer waitGroup.Done()
			result, err := services[index%len(services)].ProcessTransaction(ctx, command)
			if err != nil {
				errorsChannel <- err
				return
			}
			replays <- result.IdempotentReplay
		}(index)
	}
	waitGroup.Wait()
	close(errorsChannel)
	close(replays)

	for err := range errorsChannel {
		t.Fatalf("concurrent error = %v", err)
	}

	nonReplays := 0
	for replay := range replays {
		if !replay {
			nonReplays++
		}
	}
	if nonReplays != 1 {
		t.Fatalf("non-replays = %d, want 1", nonReplays)
	}

	assertCount(t, ctx, pool1, `SELECT COUNT(*) FROM wager_transactions WHERE external_transaction_id = 'bet-three-instances'`, 1)
	assertCount(t, ctx, pool1, `SELECT COUNT(*) FROM wallet_ledger_entries WHERE transaction_id IN (SELECT id FROM wager_transactions WHERE external_transaction_id = 'bet-three-instances')`, 1)

	var balanceMinor, version int64
	if err := pool1.QueryRow(ctx, `SELECT balance_minor, version FROM wallets WHERE id = $1`, wallet.ID().String()).Scan(&balanceMinor, &version); err != nil {
		t.Fatalf("query wallet: %v", err)
	}
	if balanceMinor != 9000 || version != 2 {
		t.Fatalf("wallet = %d v%d, want 9000 v2", balanceMinor, version)
	}
}

func TestWinReferenceValidation(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	walletService := walletapp.NewService(postgres.NewUnitOfWork(pool))
	wageringService := wageringapp.NewService(postgres.NewUnitOfWork(pool))
	playerID := uuid.New()

	wallet := createTestWallet(t, ctx, walletService, playerID, "100.00")
	bet, err := wageringService.ProcessTransaction(ctx, processCommand("provider-a", "bet-ref", "idem-bet-ref", playerID, wallet.ID(), "round-1", "game-1", domainwagering.KindBet, "10.00", ""))
	if err != nil {
		t.Fatalf("BET error = %v", err)
	}

	win, err := wageringService.ProcessTransaction(ctx, processCommand("provider-a", "win-ref", "idem-win-ref", playerID, wallet.ID(), "round-1", "game-1", domainwagering.KindWin, "5.00", "bet-ref"))
	if err != nil {
		t.Fatalf("WIN with reference error = %v", err)
	}
	assertProcessed(t, win, "95.00")
	if win.Transaction.ReferenceTransactionID() == nil || *win.Transaction.ReferenceTransactionID() != bet.Transaction.ID() {
		t.Fatal("win reference transaction id was not stored")
	}

	wrongRound, err := wageringService.ProcessTransaction(ctx, processCommand("provider-a", "win-wrong-round", "idem-win-wrong-round", playerID, wallet.ID(), "round-2", "game-1", domainwagering.KindWin, "5.00", "bet-ref"))
	if err != nil {
		t.Fatalf("WIN wrong round error = %v", err)
	}
	assertRejected(t, wrongRound, "REFERENCE_MISMATCH")

	missing, err := wageringService.ProcessTransaction(ctx, processCommand("provider-a", "win-missing", "idem-win-missing", playerID, wallet.ID(), "round-1", "game-1", domainwagering.KindWin, "5.00", "missing-reference"))
	if err != nil {
		t.Fatalf("WIN missing reference error = %v", err)
	}
	assertRejected(t, missing, "REFERENCE_NOT_FOUND")

	var balanceMinor int64
	if err := pool.QueryRow(ctx, `SELECT balance_minor FROM wallets WHERE id = $1`, wallet.ID().String()).Scan(&balanceMinor); err != nil {
		t.Fatalf("query wallet: %v", err)
	}
	if balanceMinor != 9500 {
		t.Fatalf("balance = %d, want 9500", balanceMinor)
	}
}

func TestWageringHTTPContract(t *testing.T) {
	pool := newTestPool(t)
	unitOfWork := postgres.NewUnitOfWork(pool)
	walletService := walletapp.NewService(unitOfWork)
	wageringService := wageringapp.NewService(unitOfWork)
	server := newTestHTTPServer(t, pool, walletService, wageringService)
	defer server.Close()
	client := server.Client()

	internalHeaders := authHeaders(internalToken(t))
	providerHeaders := authHeaders(providerAToken(t))

	playerID := uuid.New()
	createResponse, createBody := doJSON(t, client, http.MethodPost, server.URL+"/wallets", internalHeaders, map[string]any{
		"playerId":       playerID.String(),
		"initialBalance": map[string]string{"amount": "1000.00", "currency": "BRL"},
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

	body := processRequestBody("provider-a", "http-bet-1", playerID.String(), wallet.ID, "round-1", "game-1", "BET", "25.00", "")
	headers := mergeHeaders(providerHeaders, map[string]string{"Idempotency-Key": "provider-a:http-bet-1"})

	processResponse, processBody := doJSON(t, client, http.MethodPost, server.URL+"/wagering/transactions", headers, body)
	if processResponse.StatusCode != http.StatusOK {
		t.Fatalf("process status = %d, body = %s", processResponse.StatusCode, processBody)
	}
	var processed struct {
		TransactionID    string       `json:"transactionId"`
		Status           string       `json:"status"`
		Balance          *money.Money `json:"balance"`
		IdempotentReplay bool         `json:"idempotentReplay"`
	}
	if err := json.Unmarshal(processBody, &processed); err != nil {
		t.Fatalf("decode process response: %v", err)
	}
	if processed.Status != "PROCESSED" || processed.Balance == nil || processed.Balance.Amount() != "975.00" || processed.IdempotentReplay {
		t.Fatalf("unexpected process response: %+v", processed)
	}

	replayResponse, replayBody := doJSON(t, client, http.MethodPost, server.URL+"/wagering/transactions", headers, body)
	if replayResponse.StatusCode != http.StatusOK {
		t.Fatalf("replay status = %d, body = %s", replayResponse.StatusCode, replayBody)
	}
	var replayed struct {
		IdempotentReplay bool `json:"idempotentReplay"`
	}
	if err := json.Unmarshal(replayBody, &replayed); err != nil {
		t.Fatalf("decode replay: %v", err)
	}
	if !replayed.IdempotentReplay {
		t.Fatal("expected idempotentReplay=true")
	}

	getResponse, getBody := doJSON(t, client, http.MethodGet, server.URL+"/wagering/transactions/"+processed.TransactionID, providerHeaders, nil)
	if getResponse.StatusCode != http.StatusOK {
		t.Fatalf("get transaction status = %d, body = %s", getResponse.StatusCode, getBody)
	}
	var fetched struct {
		Status string `json:"status"`
		Kind   string `json:"kind"`
	}
	if err := json.Unmarshal(getBody, &fetched); err != nil {
		t.Fatalf("decode transaction: %v", err)
	}
	if fetched.Status != "PROCESSED" || fetched.Kind != "BET" {
		t.Fatalf("unexpected transaction: %+v", fetched)
	}

	providerResponse, providerBody := doJSON(t, client, http.MethodGet, server.URL+"/providers/provider-a/wagering/transactions/http-bet-1", providerHeaders, nil)
	if providerResponse.StatusCode != http.StatusOK {
		t.Fatalf("get provider transaction status = %d, body = %s", providerResponse.StatusCode, providerBody)
	}

	conflictResponse, _ := doJSON(t, client, http.MethodPost, server.URL+"/wagering/transactions", headers, processRequestBody("provider-a", "http-bet-1", playerID.String(), wallet.ID, "round-1", "game-1", "BET", "26.00", ""))
	if conflictResponse.StatusCode != http.StatusConflict {
		t.Fatalf("conflict status = %d, want 409", conflictResponse.StatusCode)
	}

	missingKeyResponse, _ := doJSON(t, client, http.MethodPost, server.URL+"/wagering/transactions", providerHeaders, body)
	if missingKeyResponse.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing key status = %d, want 400", missingKeyResponse.StatusCode)
	}

	lossResponse, _ := doJSON(t, client, http.MethodPost, server.URL+"/wagering/transactions", mergeHeaders(providerHeaders, map[string]string{"Idempotency-Key": "provider-a:http-loss-invalid"}), processRequestBody("provider-a", "http-loss-invalid", playerID.String(), wallet.ID, "round-1", "game-1", "LOSS", "1.00", ""))
	if lossResponse.StatusCode != http.StatusBadRequest {
		t.Fatalf("loss non-zero status = %d, want 400", lossResponse.StatusCode)
	}

	openingResponse, _ := doJSON(t, client, http.MethodPost, server.URL+"/wagering/transactions", mergeHeaders(providerHeaders, map[string]string{"Idempotency-Key": "provider-a:http-opening"}), processRequestBody("provider-a", "http-opening", playerID.String(), wallet.ID, "round-1", "game-1", "OPENING", "1.00", ""))
	if openingResponse.StatusCode != http.StatusBadRequest {
		t.Fatalf("opening status = %d, want 400", openingResponse.StatusCode)
	}

	secondWallet := createTestWallet(t, context.Background(), walletService, uuid.New(), "10.00")
	insufficientResponse, insufficientBody := doJSON(t, client, http.MethodPost, server.URL+"/wagering/transactions", mergeHeaders(providerHeaders, map[string]string{"Idempotency-Key": "provider-a:http-insufficient"}), processRequestBody("provider-a", "http-insufficient", secondWallet.PlayerID().String(), secondWallet.ID().String(), "round-1", "game-1", "BET", "20.00", ""))
	if insufficientResponse.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("insufficient status = %d, body = %s", insufficientResponse.StatusCode, insufficientBody)
	}
	var rejected struct {
		FailureCode string `json:"failureCode"`
	}
	if err := json.Unmarshal(insufficientBody, &rejected); err != nil {
		t.Fatalf("decode rejection: %v", err)
	}
	if rejected.FailureCode != "INSUFFICIENT_FUNDS" {
		t.Fatalf("failure code = %q, want INSUFFICIENT_FUNDS", rejected.FailureCode)
	}
}

func createTestWallet(t *testing.T, ctx context.Context, service *walletapp.Service, playerID uuid.UUID, amount string) *domainwallet.Wallet {
	t.Helper()
	initial, err := money.Parse(amount, "BRL")
	if err != nil {
		t.Fatalf("parse initial balance: %v", err)
	}
	created, err := service.CreateWallet(ctx, walletapp.CreateWalletCommand{
		PlayerID:       playerID,
		InitialBalance: initial,
		CorrelationID:  "test-correlation",
	})
	if err != nil {
		t.Fatalf("create wallet: %v", err)
	}
	return created
}

func processCommand(
	providerID, externalID, idempotencyKey string,
	playerID, walletID uuid.UUID,
	roundID, gameID string,
	kind domainwagering.Kind,
	amount, reference string,
) wageringapp.ProcessCommand {
	parsed, _ := money.Parse(amount, "BRL")
	return wageringapp.ProcessCommand{
		ProviderID:                     providerID,
		ExternalTransactionID:          externalID,
		IdempotencyKey:                 idempotencyKey,
		PlayerID:                       playerID,
		WalletID:                       walletID,
		RoundID:                        roundID,
		GameID:                         gameID,
		Kind:                           kind,
		Amount:                         parsed,
		ReferenceExternalTransactionID: reference,
		CorrelationID:                  "test-correlation",
	}
}

func processRequestBody(providerID, externalID, playerID, walletID, roundID, gameID, kind, amount, reference string) map[string]any {
	body := map[string]any{
		"providerId":            providerID,
		"externalTransactionId": externalID,
		"playerId":              playerID,
		"walletId":              walletID,
		"roundId":               roundID,
		"gameId":                gameID,
		"kind":                  kind,
		"money":                 map[string]string{"amount": amount, "currency": "BRL"},
	}
	if reference != "" {
		body["referenceExternalTransactionId"] = reference
	}
	return body
}

func assertProcessed(t *testing.T, result *wageringapp.ProcessResult, balance string) {
	t.Helper()
	if result.Transaction.Status() != domainwagering.StatusProcessed {
		t.Fatalf("status = %q, want PROCESSED", result.Transaction.Status())
	}
	if result.Balance == nil || result.Balance.Amount() != balance {
		t.Fatalf("balance = %v, want %s", result.Balance, balance)
	}
}

func assertRejected(t *testing.T, result *wageringapp.ProcessResult, failureCode string) {
	t.Helper()
	if result.Transaction.Status() != domainwagering.StatusRejected {
		t.Fatalf("status = %q, want REJECTED", result.Transaction.Status())
	}
	if result.Transaction.FailureCode() == nil || *result.Transaction.FailureCode() != failureCode {
		t.Fatalf("failure code = %v, want %s", result.Transaction.FailureCode(), failureCode)
	}
	if result.Balance != nil {
		t.Fatal("rejection must not return balance")
	}
}

func doJSON(t *testing.T, client *http.Client, method, url string, headers map[string]string, body any) (*http.Response, []byte) {
	t.Helper()

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}

	request, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}

	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	response.Body.Close()
	return response, responseBody
}
