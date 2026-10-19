package sqs

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	sqsservice "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/google/uuid"

	"github.com/TheProzin/Jungle-Go/internal/app/outbox"
	"github.com/TheProzin/Jungle-Go/internal/app/ports"
	"github.com/TheProzin/Jungle-Go/internal/config"
)

type EventSender struct {
	client   *sqsservice.Client
	queueURL string
}

type eventEnvelope struct {
	EventID       uuid.UUID       `json:"eventId"`
	EventType     string          `json:"eventType"`
	Version       int             `json:"version"`
	AggregateID   string          `json:"aggregateId"`
	CorrelationID string          `json:"correlationId"`
	CausationID   *string         `json:"causationId,omitempty"`
	OccurredAt    time.Time       `json:"occurredAt"`
	Data          json.RawMessage `json:"data"`
}

func NewEventSender(client *sqsservice.Client, cfg *config.Config) outbox.Sender {
	return &EventSender{
		client:   client,
		queueURL: cfg.SQSEventsQueueURL,
	}
}

func (s *EventSender) Send(ctx context.Context, event ports.OutboxEvent) error {
	envelope := eventEnvelope{
		EventID:       event.ID,
		EventType:     event.EventType,
		Version:       event.EventVersion,
		AggregateID:   event.AggregateID,
		CorrelationID: event.CorrelationID,
		CausationID:   event.CausationID,
		OccurredAt:    event.OccurredAt,
		Data:          json.RawMessage(event.Payload),
	}

	body, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("marshal event envelope: %w", err)
	}

	_, err = s.client.SendMessage(ctx, &sqsservice.SendMessageInput{
		QueueUrl:               aws.String(s.queueURL),
		MessageBody:            aws.String(string(body)),
		MessageGroupId:         aws.String(event.AggregateID),
		MessageDeduplicationId: aws.String(event.ID.String()),
	})
	if err != nil {
		return fmt.Errorf("send event to sqs: %w", err)
	}
	return nil
}
