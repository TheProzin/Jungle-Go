package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TheProzin/Jungle-Go/internal/app/ports"
	"github.com/TheProzin/Jungle-Go/internal/domain/events"
)

type OutboxRepository struct {
	query querier
}

func (r *OutboxRepository) Insert(ctx context.Context, event events.Event) error {
	payload, err := json.Marshal(event.Data())
	if err != nil {
		return fmt.Errorf("marshal outbox payload: %w", err)
	}

	_, err = r.query.Exec(ctx, `
		INSERT INTO outbox_events (
			event_id, aggregate_type, aggregate_id, event_type, event_version, payload,
			correlation_id, causation_id, occurred_at, attempts, next_attempt_at, created_at
		) VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8, $9, 0, $9, $9)
	`,
		event.ID().String(),
		event.AggregateType(),
		event.AggregateID(),
		event.Type(),
		event.Version(),
		string(payload),
		event.CorrelationID(),
		event.CausationID(),
		event.OccurredAt(),
	)
	if err != nil {
		return fmt.Errorf("insert outbox event: %w", err)
	}
	return nil
}

func (r *OutboxRepository) ClaimBatch(
	ctx context.Context,
	limit int,
	lease time.Duration,
) ([]ports.OutboxEvent, error) {
	rows, err := r.query.Query(ctx, `
		UPDATE outbox_events
		SET lease_until = now() + ($2 * interval '1 second'),
		    attempts = attempts + 1
		WHERE event_id IN (
			SELECT event_id
			FROM outbox_events
			WHERE published_at IS NULL
			  AND next_attempt_at <= now()
			  AND (lease_until IS NULL OR lease_until < now())
			ORDER BY occurred_at
			FOR UPDATE SKIP LOCKED
			LIMIT $1
		)
		RETURNING event_id, aggregate_type, aggregate_id, event_type, event_version,
		          payload, correlation_id, causation_id, occurred_at, attempts
	`, limit, lease.Seconds())
	if err != nil {
		return nil, fmt.Errorf("claim outbox events: %w", err)
	}
	defer rows.Close()

	events := make([]ports.OutboxEvent, 0, limit)
	for rows.Next() {
		var (
			eventID       string
			aggregateType string
			aggregateID   string
			eventType     string
			eventVersion  int
			payload       []byte
			correlationID string
			causationID   *string
			occurredAt    time.Time
			attempts      int
		)
		if err := rows.Scan(
			&eventID,
			&aggregateType,
			&aggregateID,
			&eventType,
			&eventVersion,
			&payload,
			&correlationID,
			&causationID,
			&occurredAt,
			&attempts,
		); err != nil {
			return nil, fmt.Errorf("scan outbox event: %w", err)
		}

		parsedID, err := uuid.Parse(eventID)
		if err != nil {
			return nil, fmt.Errorf("parse outbox event id: %w", err)
		}

		events = append(events, ports.OutboxEvent{
			ID:            parsedID,
			AggregateType: aggregateType,
			AggregateID:   aggregateID,
			EventType:     eventType,
			EventVersion:  eventVersion,
			Payload:       payload,
			CorrelationID: correlationID,
			CausationID:   causationID,
			OccurredAt:    occurredAt,
			Attempts:      attempts,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate outbox events: %w", err)
	}
	return events, nil
}

func (r *OutboxRepository) MarkPublished(
	ctx context.Context,
	eventID uuid.UUID,
	publishedAt time.Time,
) error {
	_, err := r.query.Exec(ctx, `
		UPDATE outbox_events
		SET published_at = $2, lease_until = NULL, last_error = NULL
		WHERE event_id = $1
	`, eventID.String(), publishedAt)
	if err != nil {
		return fmt.Errorf("mark outbox event published: %w", err)
	}
	return nil
}

func (r *OutboxRepository) MarkFailed(
	ctx context.Context,
	eventID uuid.UUID,
	nextAttemptAt time.Time,
	lastError string,
) error {
	_, err := r.query.Exec(ctx, `
		UPDATE outbox_events
		SET lease_until = NULL, next_attempt_at = $2, last_error = $3
		WHERE event_id = $1
	`, eventID.String(), nextAttemptAt, lastError)
	if err != nil {
		return fmt.Errorf("mark outbox event failed: %w", err)
	}
	return nil
}
