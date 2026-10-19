package events

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TheProzin/Jungle-Go/internal/domain/ledger"
	"github.com/TheProzin/Jungle-Go/internal/domain/money"
)

const (
	TypeWagerTransactionProcessed        = "WagerTransactionProcessed"
	TypeWagerTransactionRejected         = "WagerTransactionRejected"
	TypeWalletBalanceChanged             = "WalletBalanceChanged"
	TypeWagerTransactionPendingReference = "WagerTransactionPendingReference"

	EventVersion = 1

	AggregateWagerTransaction = "wager_transaction"
	AggregateWallet           = "wallet"
)

var ErrInvalidEvent = errors.New("invalid event")

type Event struct {
	id            uuid.UUID
	eventType     string
	version       int
	aggregateType string
	aggregateID   string
	correlationID string
	causationID   *string
	occurredAt    time.Time
	data          any
}

type WagerTransactionProcessedData struct {
	TransactionID         string      `json:"transactionId"`
	ProviderID            string      `json:"providerId,omitempty"`
	ExternalTransactionID string      `json:"externalTransactionId,omitempty"`
	Kind                  string      `json:"kind"`
	WalletID              string      `json:"walletId"`
	PlayerID              string      `json:"playerId"`
	RoundID               string      `json:"roundId,omitempty"`
	GameID                string      `json:"gameId,omitempty"`
	Money                 money.Money `json:"money"`
	Status                string      `json:"status"`
}

type WagerTransactionRejectedData struct {
	TransactionID         string `json:"transactionId"`
	ProviderID            string `json:"providerId,omitempty"`
	ExternalTransactionID string `json:"externalTransactionId,omitempty"`
	Kind                  string `json:"kind"`
	FailureCode           string `json:"failureCode"`
}

type WalletBalanceChangedData struct {
	WalletID      string           `json:"walletId"`
	TransactionID string           `json:"transactionId"`
	Direction     ledger.Direction `json:"direction"`
	Money         money.Money      `json:"money"`
	BalanceBefore money.Money      `json:"balanceBefore"`
	BalanceAfter  money.Money      `json:"balanceAfter"`
	WalletVersion int64            `json:"walletVersion"`
}

type WagerTransactionPendingReferenceData struct {
	TransactionID                  string    `json:"transactionId"`
	ReferenceExternalTransactionID string    `json:"referenceExternalTransactionId"`
	Attempts                       int       `json:"attempts"`
	NextAttemptAt                  time.Time `json:"nextAttemptAt"`
}

func NewWagerTransactionProcessed(
	eventID uuid.UUID,
	aggregateID, correlationID string,
	causationID *string,
	occurredAt time.Time,
	data WagerTransactionProcessedData,
) (Event, error) {
	if !data.Money.IsValid() {
		return Event{}, fmt.Errorf("%w: invalid money", ErrInvalidEvent)
	}
	return newEvent(
		eventID,
		TypeWagerTransactionProcessed,
		AggregateWagerTransaction,
		aggregateID,
		correlationID,
		causationID,
		occurredAt,
		data,
	)
}

func NewWagerTransactionRejected(
	eventID uuid.UUID,
	aggregateID, correlationID string,
	causationID *string,
	occurredAt time.Time,
	data WagerTransactionRejectedData,
) (Event, error) {
	if data.FailureCode == "" {
		return Event{}, fmt.Errorf("%w: failure code is required", ErrInvalidEvent)
	}
	return newEvent(
		eventID,
		TypeWagerTransactionRejected,
		AggregateWagerTransaction,
		aggregateID,
		correlationID,
		causationID,
		occurredAt,
		data,
	)
}

func NewWalletBalanceChanged(
	eventID uuid.UUID,
	walletID, correlationID string,
	causationID *string,
	occurredAt time.Time,
	data WalletBalanceChangedData,
) (Event, error) {
	if data.Direction != ledger.DirectionDebit && data.Direction != ledger.DirectionCredit {
		return Event{}, fmt.Errorf("%w: invalid direction", ErrInvalidEvent)
	}
	if !data.Money.IsValid() || !data.Money.IsPositive() {
		return Event{}, fmt.Errorf("%w: invalid money", ErrInvalidEvent)
	}
	if !data.BalanceBefore.IsValid() || !data.BalanceAfter.IsValid() {
		return Event{}, fmt.Errorf("%w: invalid balances", ErrInvalidEvent)
	}
	if data.BalanceBefore.IsNegative() || data.BalanceAfter.IsNegative() {
		return Event{}, fmt.Errorf("%w: negative balances", ErrInvalidEvent)
	}
	if data.WalletVersion < 1 {
		return Event{}, fmt.Errorf("%w: wallet version must be positive", ErrInvalidEvent)
	}
	return newEvent(
		eventID,
		TypeWalletBalanceChanged,
		AggregateWallet,
		walletID,
		correlationID,
		causationID,
		occurredAt,
		data,
	)
}

func NewWagerTransactionPendingReference(
	eventID uuid.UUID,
	aggregateID, correlationID string,
	causationID *string,
	occurredAt time.Time,
	data WagerTransactionPendingReferenceData,
) (Event, error) {
	if data.ReferenceExternalTransactionID == "" {
		return Event{}, fmt.Errorf("%w: reference is required", ErrInvalidEvent)
	}
	if data.Attempts < 0 {
		return Event{}, fmt.Errorf("%w: attempts cannot be negative", ErrInvalidEvent)
	}
	if data.NextAttemptAt.IsZero() {
		return Event{}, fmt.Errorf("%w: next attempt is required", ErrInvalidEvent)
	}
	return newEvent(
		eventID,
		TypeWagerTransactionPendingReference,
		AggregateWagerTransaction,
		aggregateID,
		correlationID,
		causationID,
		occurredAt,
		data,
	)
}

func newEvent(
	eventID uuid.UUID,
	eventType, aggregateType, aggregateID, correlationID string,
	causationID *string,
	occurredAt time.Time,
	data any,
) (Event, error) {
	if eventID == uuid.Nil {
		return Event{}, fmt.Errorf("%w: event id is required", ErrInvalidEvent)
	}
	if eventType == "" || aggregateType == "" || aggregateID == "" {
		return Event{}, fmt.Errorf("%w: type and aggregate are required", ErrInvalidEvent)
	}
	if correlationID == "" {
		return Event{}, fmt.Errorf("%w: correlation id is required", ErrInvalidEvent)
	}
	if occurredAt.IsZero() {
		return Event{}, fmt.Errorf("%w: occurrence time is required", ErrInvalidEvent)
	}
	if data == nil {
		return Event{}, fmt.Errorf("%w: data is required", ErrInvalidEvent)
	}

	return Event{
		id:            eventID,
		eventType:     eventType,
		version:       EventVersion,
		aggregateType: aggregateType,
		aggregateID:   aggregateID,
		correlationID: correlationID,
		causationID:   causationID,
		occurredAt:    occurredAt.UTC(),
		data:          data,
	}, nil
}

func (e Event) ID() uuid.UUID {
	return e.id
}

func (e Event) Type() string {
	return e.eventType
}

func (e Event) Version() int {
	return e.version
}

func (e Event) AggregateType() string {
	return e.aggregateType
}

func (e Event) AggregateID() string {
	return e.aggregateID
}

func (e Event) CorrelationID() string {
	return e.correlationID
}

func (e Event) CausationID() *string {
	return e.causationID
}

func (e Event) OccurredAt() time.Time {
	return e.occurredAt
}

func (e Event) Data() any {
	return e.data
}
