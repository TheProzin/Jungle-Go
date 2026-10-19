//go:build integration

package integration

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	outboxapp "github.com/TheProzin/Jungle-Go/internal/app/outbox"
	wageringapp "github.com/TheProzin/Jungle-Go/internal/app/wagering"
	walletapp "github.com/TheProzin/Jungle-Go/internal/app/wallet"
	"github.com/TheProzin/Jungle-Go/internal/config"
	domainwagering "github.com/TheProzin/Jungle-Go/internal/domain/wagering"
	"github.com/TheProzin/Jungle-Go/internal/infra/postgres"
	sqsinfra "github.com/TheProzin/Jungle-Go/internal/infra/sqs"
	"github.com/TheProzin/Jungle-Go/internal/platform/failpoint"
)

func TestSQSCrashAfterCommitIsRecovered(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	unitOfWork := postgres.NewUnitOfWork(pool)
	walletService := walletapp.NewService(unitOfWork)
	wageringService := wageringapp.NewServiceWithConfig(unitOfWork, referenceConfig())
	playerID := uuid.New()
	wallet := createTestWallet(t, ctx, walletService, playerID, "100.00")

	cfg := localStackConfig()
	client, err := sqsinfra.NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	queueURL, dlqURL := newTestQueue(t, client)
	cfg.SQSTransactionsQueueURL = queueURL
	cfg.SQSTransactionsDLQURL = dlqURL

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	consumer := sqsinfra.NewConsumer(client, cfg, wageringService, logger)

	externalID := "crash-after-commit-" + uuid.NewString()
	idempotencyKey := "crash-after-commit-key-" + uuid.NewString()
	body := envelopeJSON(t, "crash-message-"+uuid.NewString(), "provider-a", externalID, idempotencyKey, playerID.String(), wallet.ID().String(), "round-1", "game-1", "BET", "10.00", "")

	failpoint.Enable("sqs-after-commit", func() error {
		return errors.New("simulated crash after commit")
	})
	defer failpoint.Reset()

	sendSQSMessage(t, client, queueURL, wallet.ID().String(), idempotencyKey, body)
	processUntil(t, consumer, 10*time.Second)

	assertProcessedWallet(t, ctx, pool, wallet.ID().String(), 9000)
	assertCount(t, ctx, pool, `SELECT COUNT(*) FROM inbox_messages WHERE message_id LIKE 'crash-message-%' AND status = 'COMPLETED'`, 1)

	failpoint.Disable("sqs-after-commit")
	processUntil(t, consumer, 10*time.Second)

	assertProcessedWallet(t, ctx, pool, wallet.ID().String(), 9000)
	assertCount(t, ctx, pool, `SELECT COUNT(*) FROM wager_transactions WHERE external_transaction_id = '`+externalID+`'`, 1)
	assertCount(t, ctx, pool, `SELECT COUNT(*) FROM wallet_ledger_entries WHERE transaction_id IN (SELECT id FROM wager_transactions WHERE external_transaction_id = '`+externalID+`')`, 1)
}

func TestOutboxCrashAfterPublishIsRecovered(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	unitOfWork := postgres.NewUnitOfWork(pool)
	walletService := walletapp.NewService(unitOfWork)
	createTestWallet(t, ctx, walletService, uuid.New(), "100.00")

	eventIDs, err := outboxEventIDs(ctx, pool)
	if err != nil {
		t.Fatalf("outboxEventIDs() error = %v", err)
	}
	if len(eventIDs) != 2 {
		t.Fatalf("outbox events = %d, want 2", len(eventIDs))
	}

	cfg := &config.Config{
		OutboxBatchSize:         20,
		OutboxLease:             30 * time.Second,
		OutboxInitialBackoff:    100 * time.Millisecond,
		OutboxMaxBackoff:        time.Second,
		OutboxPublisherInterval: time.Second,
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sender := &fakeSender{}
	publisher := outboxapp.NewPublisher(unitOfWork, sender, cfg, logger)

	failpoint.Enable("outbox-after-publish", func() error {
		return errors.New("simulated crash after publish")
	})
	defer failpoint.Reset()

	published, err := publisher.PublishBatch(ctx)
	if err != nil {
		t.Fatalf("PublishBatch() error = %v", err)
	}
	if published != 0 {
		t.Fatalf("published = %d, want 0", published)
	}
	assertCount(t, ctx, pool, `SELECT COUNT(*) FROM outbox_events WHERE published_at IS NULL`, 2)

	failpoint.Disable("outbox-after-publish")
	if _, err := pool.Exec(ctx, `UPDATE outbox_events SET lease_until = now() - interval '1 second'`); err != nil {
		t.Fatalf("reset outbox lease: %v", err)
	}

	recoveryPublisher := outboxapp.NewPublisher(unitOfWork, sender, cfg, logger)
	published, err = recoveryPublisher.PublishBatch(ctx)
	if err != nil {
		t.Fatalf("recovery PublishBatch() error = %v", err)
	}
	if published != 2 {
		t.Fatalf("recovery published = %d, want 2", published)
	}
	assertCount(t, ctx, pool, `SELECT COUNT(*) FROM outbox_events WHERE published_at IS NULL`, 0)

	for _, eventID := range eventIDs {
		if count := sender.sentCount(uuid.MustParse(eventID)); count != 2 {
			t.Fatalf("event %s sent %d times, want 2 (same event id republished)", eventID, count)
		}
	}
}

func TestPendingReferenceCrashAfterCommitIsRecovered(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	walletService := walletapp.NewService(postgres.NewUnitOfWork(pool))
	wageringService := wageringapp.NewServiceWithConfig(postgres.NewUnitOfWork(pool), referenceConfig())
	playerID := uuid.New()
	wallet := createTestWallet(t, ctx, walletService, playerID, "100.00")

	failpoint.Enable("pending-after-commit", func() error {
		return errors.New("simulated crash after pending commit")
	})
	defer failpoint.Reset()

	_, err := wageringService.ProcessTransaction(ctx, processCommand(
		"provider-a", "refund-crash", "idem-refund-crash", playerID, wallet.ID(),
		"round-1", "game-1", domainwagering.KindRefund, "10.00", "bet-after-crash",
	))
	if err == nil {
		t.Fatal("expected failpoint error")
	}
	assertCount(t, ctx, pool, `SELECT COUNT(*) FROM wager_transactions WHERE status = 'PENDING_REFERENCE'`, 1)

	failpoint.Disable("pending-after-commit")
	if _, err := wageringService.ProcessTransaction(ctx, processCommand(
		"provider-a", "bet-after-crash", "idem-bet-after-crash", playerID, wallet.ID(),
		"round-1", "game-1", domainwagering.KindBet, "10.00", "",
	)); err != nil {
		t.Fatalf("late BET error = %v", err)
	}

	if _, err := pool.Exec(ctx, `UPDATE wager_transactions SET next_reference_at = now() - interval '1 second' WHERE status = 'PENDING_REFERENCE'`); err != nil {
		t.Fatalf("force pending reference due: %v", err)
	}

	processed, err := wageringService.ProcessNextPendingReference(ctx)
	if err != nil {
		t.Fatalf("ProcessNextPendingReference() error = %v", err)
	}
	if !processed {
		t.Fatal("expected pending reference to be resumed")
	}

	assertProcessedWallet(t, ctx, pool, wallet.ID().String(), 10000)
	assertCount(t, ctx, pool, `SELECT COUNT(*) FROM wager_transactions WHERE status = 'PENDING_REFERENCE'`, 0)
}

func referenceConfig() *config.Config {
	return &config.Config{
		ReferenceInitialBackoff: time.Millisecond,
		ReferenceMaxBackoff:     time.Millisecond,
		ReferenceMaxAttempts:    5,
		ReferenceTTL:            time.Hour,
	}
}
