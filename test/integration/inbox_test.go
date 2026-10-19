//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	wageringapp "github.com/TheProzin/Jungle-Go/internal/app/wagering"
	walletapp "github.com/TheProzin/Jungle-Go/internal/app/wallet"
	"github.com/TheProzin/Jungle-Go/internal/domain/money"
	domainwagering "github.com/TheProzin/Jungle-Go/internal/domain/wagering"
	"github.com/TheProzin/Jungle-Go/internal/infra/postgres"
)

func TestInboxDeduplicationAndHashMismatch(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	walletService := walletapp.NewService(postgres.NewUnitOfWork(pool))
	wageringService := wageringapp.NewService(postgres.NewUnitOfWork(pool))
	playerID := uuid.New()

	wallet := createTestWallet(t, ctx, walletService, playerID, "100.00")
	command := processCommand("provider-a", "inbox-bet", "inbox-key", playerID, wallet.ID(), "round-1", "game-1", domainwagering.KindBet, "10.00", "")
	identity := wageringapp.InboxIdentity{ConsumerName: "test-consumer", MessageID: "message-1"}

	first, err := wageringService.ProcessTransactionWithInbox(ctx, command, identity)
	if err != nil {
		t.Fatalf("first ProcessTransactionWithInbox() error = %v", err)
	}
	assertProcessed(t, first, "90.00")
	assertCount(t, ctx, pool, `SELECT COUNT(*) FROM inbox_messages WHERE status = 'COMPLETED'`, 1)

	if _, err := wageringService.ProcessTransactionWithInbox(ctx, command, identity); !errors.Is(err, wageringapp.ErrDuplicateMessage) {
		t.Fatalf("duplicate error = %v, want ErrDuplicateMessage", err)
	}

	changed := command
	changed.Amount, _ = money.Parse("11.00", "BRL")
	if _, err := wageringService.ProcessTransactionWithInbox(ctx, changed, identity); !errors.Is(err, wageringapp.ErrInboxHashMismatch) {
		t.Fatalf("hash mismatch error = %v, want ErrInboxHashMismatch", err)
	}

	assertCount(t, ctx, pool, `SELECT COUNT(*) FROM wager_transactions WHERE external_transaction_id = 'inbox-bet'`, 1)
	assertCount(t, ctx, pool, `SELECT COUNT(*) FROM wallet_ledger_entries WHERE transaction_id = '`+first.Transaction.ID().String()+`'`, 1)

	var balanceMinor int64
	if err := pool.QueryRow(ctx, `SELECT balance_minor FROM wallets WHERE id = $1`, wallet.ID().String()).Scan(&balanceMinor); err != nil {
		t.Fatalf("query wallet: %v", err)
	}
	if balanceMinor != 9000 {
		t.Fatalf("balance = %d, want 9000", balanceMinor)
	}
}

func TestInboxCompletesOnBusinessRejection(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	walletService := walletapp.NewService(postgres.NewUnitOfWork(pool))
	wageringService := wageringapp.NewService(postgres.NewUnitOfWork(pool))
	playerID := uuid.New()

	wallet := createTestWallet(t, ctx, walletService, playerID, "10.00")
	command := processCommand("provider-a", "inbox-insufficient", "inbox-insufficient-key", playerID, wallet.ID(), "round-1", "game-1", domainwagering.KindBet, "20.00", "")

	result, err := wageringService.ProcessTransactionWithInbox(
		ctx,
		command,
		wageringapp.InboxIdentity{ConsumerName: "test-consumer", MessageID: "message-rejected"},
	)
	if err != nil {
		t.Fatalf("ProcessTransactionWithInbox() error = %v", err)
	}
	assertRejected(t, result, "INSUFFICIENT_FUNDS")
	assertCount(t, ctx, pool, `SELECT COUNT(*) FROM inbox_messages WHERE status = 'COMPLETED'`, 1)
}

func TestInboxCompletesOnPendingReference(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	walletService := walletapp.NewService(postgres.NewUnitOfWork(pool))
	wageringService := wageringapp.NewService(postgres.NewUnitOfWork(pool))
	playerID := uuid.New()

	wallet := createTestWallet(t, ctx, walletService, playerID, "100.00")
	command := processCommand("provider-a", "inbox-pending", "inbox-pending-key", playerID, wallet.ID(), "round-1", "game-1", domainwagering.KindRefund, "10.00", "missing-reference")

	result, err := wageringService.ProcessTransactionWithInbox(
		ctx,
		command,
		wageringapp.InboxIdentity{ConsumerName: "test-consumer", MessageID: "message-pending"},
	)
	if err != nil {
		t.Fatalf("ProcessTransactionWithInbox() error = %v", err)
	}
	if result.Transaction.Status() != domainwagering.StatusPendingReference {
		t.Fatalf("status = %q, want PENDING_REFERENCE", result.Transaction.Status())
	}
	assertCount(t, ctx, pool, `SELECT COUNT(*) FROM inbox_messages WHERE status = 'COMPLETED'`, 1)
	assertCount(t, ctx, pool, `SELECT COUNT(*) FROM wager_transactions WHERE status = 'PENDING_REFERENCE'`, 1)
}

func TestCrossChannelIdempotency(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	walletService := walletapp.NewService(postgres.NewUnitOfWork(pool))
	wageringService := wageringapp.NewService(postgres.NewUnitOfWork(pool))

	t.Run("HTTP then SQS", func(t *testing.T) {
		playerID := uuid.New()
		wallet := createTestWallet(t, ctx, walletService, playerID, "100.00")
		command := processCommand("provider-a", "cross-http-sqs", "idem-cross-http-sqs", playerID, wallet.ID(), "round-1", "game-1", domainwagering.KindBet, "10.00", "")

		first, err := wageringService.ProcessTransaction(ctx, command)
		if err != nil {
			t.Fatalf("HTTP ProcessTransaction() error = %v", err)
		}
		assertProcessed(t, first, "90.00")

		second, err := wageringService.ProcessTransactionWithInbox(
			ctx,
			command,
			wageringapp.InboxIdentity{ConsumerName: "test-consumer", MessageID: "cross-message-1"},
		)
		if err != nil {
			t.Fatalf("SQS ProcessTransactionWithInbox() error = %v", err)
		}
		if !second.IdempotentReplay || second.Transaction.ID() != first.Transaction.ID() {
			t.Fatalf("second result = %+v, want replay of %s", second, first.Transaction.ID())
		}
		if second.Balance == nil || second.Balance.Amount() != "90.00" {
			t.Fatalf("second balance = %v, want 90.00", second.Balance)
		}

		assertCount(t, ctx, pool, `SELECT COUNT(*) FROM wager_transactions WHERE external_transaction_id = 'cross-http-sqs'`, 1)
		assertCount(t, ctx, pool, `SELECT COUNT(*) FROM wallet_ledger_entries WHERE transaction_id = '`+first.Transaction.ID().String()+`'`, 1)
		assertCount(t, ctx, pool, `SELECT COUNT(*) FROM inbox_messages WHERE message_id = 'cross-message-1' AND status = 'COMPLETED'`, 1)
	})

	t.Run("SQS then HTTP", func(t *testing.T) {
		playerID := uuid.New()
		wallet := createTestWallet(t, ctx, walletService, playerID, "100.00")
		command := processCommand("provider-a", "cross-sqs-http", "idem-cross-sqs-http", playerID, wallet.ID(), "round-1", "game-1", domainwagering.KindBet, "10.00", "")

		first, err := wageringService.ProcessTransactionWithInbox(
			ctx,
			command,
			wageringapp.InboxIdentity{ConsumerName: "test-consumer", MessageID: "cross-message-2"},
		)
		if err != nil {
			t.Fatalf("SQS ProcessTransactionWithInbox() error = %v", err)
		}
		assertProcessed(t, first, "90.00")

		second, err := wageringService.ProcessTransaction(ctx, command)
		if err != nil {
			t.Fatalf("HTTP ProcessTransaction() error = %v", err)
		}
		if !second.IdempotentReplay || second.Transaction.ID() != first.Transaction.ID() {
			t.Fatalf("second result = %+v, want replay of %s", second, first.Transaction.ID())
		}
		if second.Balance == nil || second.Balance.Amount() != "90.00" {
			t.Fatalf("second balance = %v, want 90.00", second.Balance)
		}

		assertCount(t, ctx, pool, `SELECT COUNT(*) FROM wager_transactions WHERE external_transaction_id = 'cross-sqs-http'`, 1)
		assertCount(t, ctx, pool, `SELECT COUNT(*) FROM wallet_ledger_entries WHERE transaction_id = '`+first.Transaction.ID().String()+`'`, 1)
		assertCount(t, ctx, pool, `SELECT COUNT(*) FROM inbox_messages WHERE message_id = 'cross-message-2' AND status = 'COMPLETED'`, 1)
	})
}

func TestInboxRollsBackWhenDomainFails(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	wageringService := wageringapp.NewService(postgres.NewUnitOfWork(pool))
	playerID := uuid.New()
	missingWalletID := uuid.New()

	command := processCommand("provider-a", "inbox-missing-wallet", "inbox-missing-wallet-key", playerID, missingWalletID, "round-1", "game-1", domainwagering.KindBet, "10.00", "")

	_, err := wageringService.ProcessTransactionWithInbox(
		ctx,
		command,
		wageringapp.InboxIdentity{ConsumerName: "test-consumer", MessageID: "message-missing-wallet"},
	)
	if err == nil {
		t.Fatal("expected wallet not found error")
	}
	assertCount(t, ctx, pool, `SELECT COUNT(*) FROM inbox_messages`, 0)
}
