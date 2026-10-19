package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type InboxRepository struct {
	query querier
}

func (r *InboxRepository) Insert(
	ctx context.Context,
	consumerName, messageID, payloadHash string,
	receivedAt time.Time,
) (bool, string, error) {
	var id int64
	err := r.query.QueryRow(ctx, `
		INSERT INTO inbox_messages (consumer_name, message_id, payload_hash, status, received_at)
		VALUES ($1, $2, $3, 'RECEIVED', $4)
		ON CONFLICT (consumer_name, message_id) DO NOTHING
		RETURNING id
	`, consumerName, messageID, payloadHash, receivedAt).Scan(&id)

	if err == nil {
		return true, "", nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, "", fmt.Errorf("insert inbox message: %w", err)
	}

	var existingHash string
	if err := r.query.QueryRow(ctx, `
		SELECT payload_hash
		FROM inbox_messages
		WHERE consumer_name = $1 AND message_id = $2
	`, consumerName, messageID).Scan(&existingHash); err != nil {
		return false, "", fmt.Errorf("query inbox message: %w", err)
	}
	return false, existingHash, nil
}

func (r *InboxRepository) Complete(
	ctx context.Context,
	consumerName, messageID string,
	completedAt time.Time,
) error {
	_, err := r.query.Exec(ctx, `
		UPDATE inbox_messages
		SET status = 'COMPLETED', completed_at = $3, last_error = NULL
		WHERE consumer_name = $1 AND message_id = $2
	`, consumerName, messageID, completedAt)
	if err != nil {
		return fmt.Errorf("complete inbox message: %w", err)
	}
	return nil
}

func (r *InboxRepository) MarkFailed(
	ctx context.Context,
	consumerName, messageID, reason string,
	completedAt time.Time,
) error {
	_, err := r.query.Exec(ctx, `
		UPDATE inbox_messages
		SET status = 'FAILED', completed_at = $4, last_error = $3
		WHERE consumer_name = $1 AND message_id = $2
	`, consumerName, messageID, reason, completedAt)
	if err != nil {
		return fmt.Errorf("fail inbox message: %w", err)
	}
	return nil
}
