//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	sqsservice "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	outboxapp "github.com/TheProzin/Jungle-Go/internal/app/outbox"
	"github.com/TheProzin/Jungle-Go/internal/app/ports"
	walletapp "github.com/TheProzin/Jungle-Go/internal/app/wallet"
	"github.com/TheProzin/Jungle-Go/internal/config"
	"github.com/TheProzin/Jungle-Go/internal/infra/postgres"
	sqsinfra "github.com/TheProzin/Jungle-Go/internal/infra/sqs"
)

type fakeSender struct {
	mu   sync.Mutex
	fail bool
	sent map[uuid.UUID]int
}

func (s *fakeSender) Send(_ context.Context, event ports.OutboxEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.sent == nil {
		s.sent = make(map[uuid.UUID]int)
	}
	s.sent[event.ID]++
	if s.fail {
		return errors.New("send failure")
	}
	return nil
}

func (s *fakeSender) sentCount(eventID uuid.UUID) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sent[eventID]
}

func TestOutboxPublisherPublishesEventsToSQS(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	uow := postgres.NewUnitOfWork(pool)
	walletService := walletapp.NewService(uow)
	createTestWallet(t, ctx, walletService, uuid.New(), "100.00")

	eventIDs, err := outboxEventIDs(ctx, pool)
	if err != nil {
		t.Fatalf("outboxEventIDs() error = %v", err)
	}
	if len(eventIDs) != 2 {
		t.Fatalf("outbox event count = %d, want 2", len(eventIDs))
	}

	cfg := localStackConfig()
	client, err := sqsinfra.NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	queueURL, _ := newTestQueue(t, client)
	cfg.SQSEventsQueueURL = queueURL
	cfg.OutboxBatchSize = 20
	cfg.OutboxLease = 30 * time.Second
	cfg.OutboxInitialBackoff = 100 * time.Millisecond
	cfg.OutboxMaxBackoff = time.Second
	cfg.OutboxPublisherInterval = time.Second

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	publisher := outboxapp.NewPublisher(uow, sqsinfra.NewEventSender(client, cfg), cfg, logger)

	published, err := publisher.PublishBatch(ctx)
	if err != nil {
		t.Fatalf("PublishBatch() error = %v", err)
	}
	if published != len(eventIDs) {
		t.Fatalf("published = %d, want %d", published, len(eventIDs))
	}

	output, err := client.ReceiveMessage(ctx, &sqsservice.ReceiveMessageInput{
		QueueUrl:            aws.String(queueURL),
		MaxNumberOfMessages: 10,
		WaitTimeSeconds:     2,
	})
	if err != nil {
		t.Fatalf("ReceiveMessage() error = %v", err)
	}
	if len(output.Messages) != len(eventIDs) {
		t.Fatalf("received messages = %d, want %d", len(output.Messages), len(eventIDs))
	}

	seen := make(map[string]bool)
	for _, message := range output.Messages {
		var envelope struct {
			EventID       string          `json:"eventId"`
			EventType     string          `json:"eventType"`
			Version       int             `json:"version"`
			AggregateID   string          `json:"aggregateId"`
			CorrelationID string          `json:"correlationId"`
			OccurredAt    time.Time       `json:"occurredAt"`
			Data          json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal([]byte(aws.ToString(message.Body)), &envelope); err != nil {
			t.Fatalf("unmarshal envelope: %v", err)
		}
		if envelope.Version != 1 {
			t.Fatalf("version = %d, want 1", envelope.Version)
		}
		if envelope.AggregateID == "" || envelope.CorrelationID == "" || envelope.OccurredAt.IsZero() {
			t.Fatalf("incomplete envelope: %+v", envelope)
		}
		if len(envelope.Data) == 0 {
			t.Fatal("missing event data")
		}
		seen[envelope.EventID] = true
	}

	for _, id := range eventIDs {
		if !seen[id] {
			t.Fatalf("event %s was not published", id)
		}
	}

	var pending int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM outbox_events WHERE published_at IS NULL`).Scan(&pending); err != nil {
		t.Fatalf("query pending outbox: %v", err)
	}
	if pending != 0 {
		t.Fatalf("pending outbox events = %d, want 0", pending)
	}
}

func TestOutboxPublisherRetriesAfterFailure(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	uow := postgres.NewUnitOfWork(pool)
	walletService := walletapp.NewService(uow)
	createTestWallet(t, ctx, walletService, uuid.New(), "100.00")

	cfg := &config.Config{
		OutboxBatchSize:         20,
		OutboxLease:             30 * time.Second,
		OutboxInitialBackoff:    100 * time.Millisecond,
		OutboxMaxBackoff:        time.Second,
		OutboxPublisherInterval: time.Second,
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	failing := &fakeSender{fail: true}
	publisher := outboxapp.NewPublisher(uow, failing, cfg, logger)
	published, err := publisher.PublishBatch(ctx)
	if err != nil {
		t.Fatalf("PublishBatch() error = %v", err)
	}
	if published != 0 {
		t.Fatalf("published = %d, want 0", published)
	}

	var (
		attempts    int
		publishedAt *time.Time
		lastError   *string
		nextAttempt time.Time
	)
	if err := pool.QueryRow(ctx, `
		SELECT attempts, published_at, last_error, next_attempt_at
		FROM outbox_events
		LIMIT 1
	`).Scan(&attempts, &publishedAt, &lastError, &nextAttempt); err != nil {
		t.Fatalf("query outbox: %v", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
	if publishedAt != nil {
		t.Fatal("published_at must be null after failure")
	}
	if lastError == nil || *lastError == "" {
		t.Fatal("last_error must be recorded")
	}
	if !nextAttempt.After(time.Now()) {
		t.Fatalf("next_attempt_at = %v, want future", nextAttempt)
	}

	if _, err := pool.Exec(ctx, `UPDATE outbox_events SET next_attempt_at = now() - interval '1 second'`); err != nil {
		t.Fatalf("reset next attempt: %v", err)
	}

	success := &fakeSender{}
	publisher = outboxapp.NewPublisher(uow, success, cfg, logger)
	published, err = publisher.PublishBatch(ctx)
	if err != nil {
		t.Fatalf("retry PublishBatch() error = %v", err)
	}
	if published != 2 {
		t.Fatalf("retry published = %d, want 2", published)
	}

	var unpublished int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM outbox_events WHERE published_at IS NULL`).Scan(&unpublished); err != nil {
		t.Fatalf("query pending outbox: %v", err)
	}
	if unpublished != 0 {
		t.Fatalf("unpublished = %d, want 0", unpublished)
	}
}

func TestConcurrentOutboxPublishersDoNotDuplicate(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	uow := postgres.NewUnitOfWork(pool)
	walletService := walletapp.NewService(uow)
	createTestWallet(t, ctx, walletService, uuid.New(), "10.00")
	createTestWallet(t, ctx, walletService, uuid.New(), "20.00")

	eventIDs, err := outboxEventIDs(ctx, pool)
	if err != nil {
		t.Fatalf("outboxEventIDs() error = %v", err)
	}
	if len(eventIDs) != 4 {
		t.Fatalf("outbox event count = %d, want 4", len(eventIDs))
	}

	cfg := &config.Config{
		OutboxBatchSize:         4,
		OutboxLease:             30 * time.Second,
		OutboxInitialBackoff:    100 * time.Millisecond,
		OutboxMaxBackoff:        time.Second,
		OutboxPublisherInterval: time.Second,
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sender := &fakeSender{}

	publisherA := outboxapp.NewPublisher(uow, sender, cfg, logger)
	publisherB := outboxapp.NewPublisher(uow, sender, cfg, logger)

	var waitGroup sync.WaitGroup
	waitGroup.Add(2)
	for _, publisher := range []*outboxapp.Publisher{publisherA, publisherB} {
		go func(p *outboxapp.Publisher) {
			defer waitGroup.Done()
			if _, err := p.PublishBatch(ctx); err != nil {
				t.Errorf("PublishBatch() error = %v", err)
			}
		}(publisher)
	}
	waitGroup.Wait()

	for _, eventID := range eventIDs {
		if count := sender.sentCount(uuid.MustParse(eventID)); count != 1 {
			t.Fatalf("event %s sent %d times, want 1", eventID, count)
		}
	}

	var published int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM outbox_events WHERE published_at IS NOT NULL`).Scan(&published); err != nil {
		t.Fatalf("query published outbox: %v", err)
	}
	if published != len(eventIDs) {
		t.Fatalf("published = %d, want %d", published, len(eventIDs))
	}
}

func outboxEventIDs(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	rows, err := pool.Query(ctx, `SELECT event_id FROM outbox_events ORDER BY occurred_at, event_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}
