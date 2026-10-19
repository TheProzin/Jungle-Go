package outbox

import (
	"context"
	"log/slog"
	"time"

	"github.com/TheProzin/Jungle-Go/internal/app/ports"
	"github.com/TheProzin/Jungle-Go/internal/config"
	"github.com/TheProzin/Jungle-Go/internal/observability"
	"github.com/TheProzin/Jungle-Go/internal/platform/failpoint"
)

type Sender interface {
	Send(ctx context.Context, event ports.OutboxEvent) error
}

type Publisher struct {
	uow         ports.UnitOfWork
	sender      Sender
	logger      *slog.Logger
	batchSize   int
	lease       time.Duration
	initialWait time.Duration
	maxWait     time.Duration
	interval    time.Duration
}

func NewPublisher(
	uow ports.UnitOfWork,
	sender Sender,
	cfg *config.Config,
	logger *slog.Logger,
) *Publisher {
	return &Publisher{
		uow:         uow,
		sender:      sender,
		logger:      logger,
		batchSize:   cfg.OutboxBatchSize,
		lease:       cfg.OutboxLease,
		initialWait: cfg.OutboxInitialBackoff,
		maxWait:     cfg.OutboxMaxBackoff,
		interval:    cfg.OutboxPublisherInterval,
	}
}

func (p *Publisher) Run(ctx context.Context) {
	for {
		for {
			published, err := p.PublishBatch(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				p.logger.Error("outbox publisher iteration failed", "error", err)
				break
			}
			if published == 0 {
				break
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(p.interval):
		}
	}
}

func (p *Publisher) PublishBatch(ctx context.Context) (int, error) {
	var events []ports.OutboxEvent
	err := p.uow.Do(ctx, func(repos ports.Repositories) error {
		claimed, err := repos.Outbox.ClaimBatch(ctx, p.batchSize, p.lease)
		if err != nil {
			return err
		}
		events = claimed
		return nil
	})
	if err != nil {
		return 0, err
	}

	published := 0
	for _, event := range events {
		if err := p.sender.Send(ctx, event); err != nil {
			observability.Default.RecordOutboxFailure()
			p.logger.Error(
				"outbox event publish failed",
				"eventId", event.ID.String(),
				"eventType", event.EventType,
				"attempts", event.Attempts,
				"error", err,
			)
			p.markFailed(event, err)
			continue
		}
		observability.Default.RecordOutboxPublishLatency(time.Since(event.OccurredAt))
		if err := failpoint.Trigger("outbox-after-publish"); err != nil {
			observability.Default.RecordOutboxFailure()
			p.logger.Error(
				"failpoint outbox-after-publish",
				"eventId", event.ID.String(),
				"error", err,
			)
			continue
		}
		p.markPublished(event)
		published++
	}

	p.refreshPending()
	return published, nil
}

func (p *Publisher) refreshPending() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var count int
	if err := p.uow.DoRead(ctx, func(repos ports.Repositories) error {
		var err error
		count, err = repos.Outbox.CountPending(ctx)
		return err
	}); err != nil {
		p.logger.Warn("refresh outbox pending metric", "error", err)
		return
	}
	observability.Default.SetOutboxPending(count)
}

func (p *Publisher) markPublished(event ports.OutboxEvent) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := p.uow.Do(ctx, func(repos ports.Repositories) error {
		return repos.Outbox.MarkPublished(ctx, event.ID, time.Now().UTC())
	}); err != nil {
		p.logger.Error("mark outbox event published", "eventId", event.ID.String(), "error", err)
	}
}

func (p *Publisher) markFailed(event ports.OutboxEvent, sendErr error) {
	nextAttemptAt := time.Now().UTC().Add(p.backoff(event.Attempts))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := p.uow.Do(ctx, func(repos ports.Repositories) error {
		return repos.Outbox.MarkFailed(ctx, event.ID, nextAttemptAt, sendErr.Error())
	}); err != nil {
		p.logger.Error("mark outbox event failed", "eventId", event.ID.String(), "error", err)
	}
}

func (p *Publisher) backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}

	delay := p.initialWait
	for i := 1; i < attempt; i++ {
		if delay >= p.maxWait/2 {
			return p.maxWait
		}
		delay *= 2
	}
	if delay > p.maxWait {
		return p.maxWait
	}
	return delay
}
