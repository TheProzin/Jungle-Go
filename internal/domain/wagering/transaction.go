package wagering

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TheProzin/Jungle-Go/internal/domain/money"
)

type Origin string

type Kind string

type Status string

const (
	OriginInternal Origin = "INTERNAL"
	OriginExternal Origin = "EXTERNAL"
)

const (
	KindOpening  Kind = "OPENING"
	KindBet      Kind = "BET"
	KindWin      Kind = "WIN"
	KindLoss     Kind = "LOSS"
	KindRefund   Kind = "REFUND"
	KindRollback Kind = "ROLLBACK"
)

const (
	StatusPending          Status = "PENDING"
	StatusPendingReference Status = "PENDING_REFERENCE"
	StatusProcessed        Status = "PROCESSED"
	StatusRejected         Status = "REJECTED"
	StatusFailed           Status = "FAILED"
)

var (
	ErrInvalidTransaction = errors.New("invalid transaction")
	ErrInvalidTransition  = errors.New("invalid transaction transition")
)

type Transaction struct {
	id                             uuid.UUID
	origin                         Origin
	kind                           Kind
	status                         Status
	providerID                     *string
	externalTransactionID          *string
	idempotencyKey                 *string
	payloadHash                    *string
	internalReference              *string
	walletID                       uuid.UUID
	playerID                       uuid.UUID
	roundID                        *string
	gameID                         *string
	amount                         money.Money
	referenceExternalTransactionID *string
	referenceTransactionID         *uuid.UUID
	failureCode                    *string
	resultBalance                  *money.Money
	referenceAttempts              int
	nextReferenceAt                *time.Time
	createdAt                      time.Time
	updatedAt                      time.Time
	processedAt                    *time.Time
}

func NewOpening(
	id uuid.UUID,
	internalReference string,
	walletID, playerID uuid.UUID,
	amount money.Money,
	now time.Time,
) (*Transaction, error) {
	if id == uuid.Nil || walletID == uuid.Nil || playerID == uuid.Nil {
		return nil, fmt.Errorf("%w: identifiers are required", ErrInvalidTransaction)
	}
	if internalReference == "" {
		return nil, fmt.Errorf("%w: internal reference is required", ErrInvalidTransaction)
	}
	if !amount.IsValid() || !amount.IsPositive() {
		return nil, fmt.Errorf("%w: amount must be positive and valid", ErrInvalidTransaction)
	}
	if now.IsZero() {
		return nil, fmt.Errorf("%w: creation time is required", ErrInvalidTransaction)
	}

	now = now.UTC()
	result := amount
	processedAt := now
	return &Transaction{
		id:                id,
		origin:            OriginInternal,
		kind:              KindOpening,
		status:            StatusProcessed,
		internalReference: stringPointer(internalReference),
		walletID:          walletID,
		playerID:          playerID,
		amount:            amount,
		resultBalance:     &result,
		createdAt:         now,
		updatedAt:         now,
		processedAt:       &processedAt,
	}, nil
}

func (t *Transaction) ID() uuid.UUID {
	return t.id
}

func (t *Transaction) Origin() Origin {
	return t.origin
}

func (t *Transaction) Kind() Kind {
	return t.kind
}

func (t *Transaction) Status() Status {
	return t.status
}

func (t *Transaction) ProviderID() *string {
	return t.providerID
}

func (t *Transaction) ExternalTransactionID() *string {
	return t.externalTransactionID
}

func (t *Transaction) IdempotencyKey() *string {
	return t.idempotencyKey
}

func (t *Transaction) PayloadHash() *string {
	return t.payloadHash
}

func (t *Transaction) InternalReference() *string {
	return t.internalReference
}

func (t *Transaction) WalletID() uuid.UUID {
	return t.walletID
}

func (t *Transaction) PlayerID() uuid.UUID {
	return t.playerID
}

func (t *Transaction) RoundID() *string {
	return t.roundID
}

func (t *Transaction) GameID() *string {
	return t.gameID
}

func (t *Transaction) Amount() money.Money {
	return t.amount
}

func (t *Transaction) ReferenceExternalTransactionID() *string {
	return t.referenceExternalTransactionID
}

func (t *Transaction) ReferenceTransactionID() *uuid.UUID {
	return t.referenceTransactionID
}

func (t *Transaction) FailureCode() *string {
	return t.failureCode
}

func (t *Transaction) ResultBalance() *money.Money {
	return t.resultBalance
}

func (t *Transaction) ReferenceAttempts() int {
	return t.referenceAttempts
}

func (t *Transaction) NextReferenceAt() *time.Time {
	return t.nextReferenceAt
}

func (t *Transaction) CreatedAt() time.Time {
	return t.createdAt
}

func (t *Transaction) UpdatedAt() time.Time {
	return t.updatedAt
}

func (t *Transaction) ProcessedAt() *time.Time {
	return t.processedAt
}

func (t *Transaction) IsTerminal() bool {
	return t.status == StatusProcessed || t.status == StatusRejected || t.status == StatusFailed
}

func stringPointer(value string) *string {
	return &value
}
