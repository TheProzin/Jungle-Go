package postgres

import (
	"context"
	"encoding/json"
	"fmt"

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
