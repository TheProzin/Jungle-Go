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
	ErrInvalidTransaction          = errors.New("invalid transaction")
	ErrInvalidTransition           = errors.New("invalid transaction transition")
	ErrTransactionNotFound         = errors.New("transaction not found")
	ErrIdempotencyKeyConflict      = errors.New("idempotency key conflict")
	ErrExternalTransactionConflict = errors.New("external transaction conflict")
	ErrDuplicateIdentity           = errors.New("duplicate transaction identity")
	ErrDuplicateReversal           = errors.New("duplicate reversal")
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

type ExternalInput struct {
	ID                             uuid.UUID
	ProviderID                     string
	ExternalTransactionID          string
	IdempotencyKey                 string
	PayloadHash                    string
	WalletID                       uuid.UUID
	PlayerID                       uuid.UUID
	RoundID                        string
	GameID                         string
	Kind                           Kind
	Amount                         money.Money
	ReferenceExternalTransactionID string
	ReferenceTransactionID         *uuid.UUID
	Now                            time.Time
}

func NewExternal(input ExternalInput) (*Transaction, error) {
	if input.ID == uuid.Nil || input.WalletID == uuid.Nil || input.PlayerID == uuid.Nil {
		return nil, fmt.Errorf("%w: identifiers are required", ErrInvalidTransaction)
	}
	if input.ProviderID == "" || input.ExternalTransactionID == "" ||
		input.IdempotencyKey == "" || input.PayloadHash == "" {
		return nil, fmt.Errorf("%w: external identity is required", ErrInvalidTransaction)
	}
	if input.RoundID == "" || input.GameID == "" {
		return nil, fmt.Errorf("%w: round and game are required", ErrInvalidTransaction)
	}
	if input.Now.IsZero() {
		return nil, fmt.Errorf("%w: creation time is required", ErrInvalidTransaction)
	}
	if input.Kind == KindOpening || !validKind(input.Kind) {
		return nil, fmt.Errorf("%w: invalid external kind %q", ErrInvalidTransaction, input.Kind)
	}
	if !input.Amount.IsValid() {
		return nil, fmt.Errorf("%w: invalid amount", ErrInvalidTransaction)
	}
	if input.Kind == KindLoss {
		if !input.Amount.IsZero() {
			return nil, fmt.Errorf("%w: loss amount must be zero", ErrInvalidTransaction)
		}
	} else if !input.Amount.IsPositive() {
		return nil, fmt.Errorf("%w: amount must be positive", ErrInvalidTransaction)
	}
	if (input.Kind == KindRefund || input.Kind == KindRollback) &&
		input.ReferenceExternalTransactionID == "" {
		return nil, fmt.Errorf("%w: reference is required", ErrInvalidTransaction)
	}
	if (input.Kind == KindBet || input.Kind == KindLoss) &&
		input.ReferenceExternalTransactionID != "" {
		return nil, fmt.Errorf("%w: reference is not allowed for %s", ErrInvalidTransaction, input.Kind)
	}

	now := input.Now.UTC()
	return &Transaction{
		id:                             input.ID,
		origin:                         OriginExternal,
		kind:                           input.Kind,
		status:                         StatusPending,
		providerID:                     stringPointer(input.ProviderID),
		externalTransactionID:          stringPointer(input.ExternalTransactionID),
		idempotencyKey:                 stringPointer(input.IdempotencyKey),
		payloadHash:                    stringPointer(input.PayloadHash),
		walletID:                       input.WalletID,
		playerID:                       input.PlayerID,
		roundID:                        stringPointer(input.RoundID),
		gameID:                         stringPointer(input.GameID),
		amount:                         input.Amount,
		referenceExternalTransactionID: stringPointerIfNotEmpty(input.ReferenceExternalTransactionID),
		referenceTransactionID:         input.ReferenceTransactionID,
		createdAt:                      now,
		updatedAt:                      now,
	}, nil
}

type RehydrateInput struct {
	ID                             uuid.UUID
	Origin                         Origin
	Kind                           Kind
	Status                         Status
	ProviderID                     *string
	ExternalTransactionID          *string
	IdempotencyKey                 *string
	PayloadHash                    *string
	InternalReference              *string
	WalletID                       uuid.UUID
	PlayerID                       uuid.UUID
	RoundID                        *string
	GameID                         *string
	Amount                         money.Money
	ReferenceExternalTransactionID *string
	ReferenceTransactionID         *uuid.UUID
	FailureCode                    *string
	ResultBalance                  *money.Money
	ReferenceAttempts              int
	NextReferenceAt                *time.Time
	CreatedAt                      time.Time
	UpdatedAt                      time.Time
	ProcessedAt                    *time.Time
}

func Rehydrate(input RehydrateInput) (*Transaction, error) {
	if input.ID == uuid.Nil || input.WalletID == uuid.Nil || input.PlayerID == uuid.Nil {
		return nil, fmt.Errorf("%w: identifiers are required", ErrInvalidTransaction)
	}
	if !validOrigin(input.Origin) || !validKind(input.Kind) || !validStatus(input.Status) {
		return nil, fmt.Errorf("%w: invalid origin, kind or status", ErrInvalidTransaction)
	}
	if !input.Amount.IsValid() || input.Amount.IsNegative() {
		return nil, fmt.Errorf("%w: invalid amount", ErrInvalidTransaction)
	}
	if input.Kind == KindLoss && !input.Amount.IsZero() {
		return nil, fmt.Errorf("%w: loss amount must be zero", ErrInvalidTransaction)
	}
	if input.Kind != KindLoss && !input.Amount.IsPositive() {
		return nil, fmt.Errorf("%w: amount must be positive", ErrInvalidTransaction)
	}
	if input.Origin == OriginInternal && input.Kind != KindOpening {
		return nil, fmt.Errorf("%w: internal transactions are openings", ErrInvalidTransaction)
	}
	if input.Kind == KindOpening && input.Origin != OriginInternal {
		return nil, fmt.Errorf("%w: opening must be internal", ErrInvalidTransaction)
	}
	if input.Origin == OriginExternal && (input.ProviderID == nil || input.ExternalTransactionID == nil ||
		input.IdempotencyKey == nil || input.PayloadHash == nil ||
		input.RoundID == nil || input.GameID == nil) {
		return nil, fmt.Errorf("%w: external metadata is required", ErrInvalidTransaction)
	}
	if input.Kind == KindOpening && input.InternalReference == nil {
		return nil, fmt.Errorf("%w: internal reference is required", ErrInvalidTransaction)
	}
	if isTerminal(input.Status) && input.ProcessedAt == nil {
		return nil, fmt.Errorf("%w: processed time is required for terminal status", ErrInvalidTransaction)
	}
	if (input.Status == StatusRejected || input.Status == StatusFailed) && input.FailureCode == nil {
		return nil, fmt.Errorf("%w: failure code is required", ErrInvalidTransaction)
	}
	if input.Status == StatusProcessed {
		if input.ResultBalance == nil || !input.ResultBalance.IsValid() || input.ResultBalance.IsNegative() {
			return nil, fmt.Errorf("%w: result balance is required", ErrInvalidTransaction)
		}
		if input.ResultBalance.Currency() != input.Amount.Currency() {
			return nil, fmt.Errorf("%w: result balance currency mismatch", ErrInvalidTransaction)
		}
	}
	if input.ReferenceAttempts < 0 {
		return nil, fmt.Errorf("%w: reference attempts cannot be negative", ErrInvalidTransaction)
	}
	if input.CreatedAt.IsZero() || input.UpdatedAt.IsZero() {
		return nil, fmt.Errorf("%w: timestamps are required", ErrInvalidTransaction)
	}

	return &Transaction{
		id:                             input.ID,
		origin:                         input.Origin,
		kind:                           input.Kind,
		status:                         input.Status,
		providerID:                     input.ProviderID,
		externalTransactionID:          input.ExternalTransactionID,
		idempotencyKey:                 input.IdempotencyKey,
		payloadHash:                    input.PayloadHash,
		internalReference:              input.InternalReference,
		walletID:                       input.WalletID,
		playerID:                       input.PlayerID,
		roundID:                        input.RoundID,
		gameID:                         input.GameID,
		amount:                         input.Amount,
		referenceExternalTransactionID: input.ReferenceExternalTransactionID,
		referenceTransactionID:         input.ReferenceTransactionID,
		failureCode:                    input.FailureCode,
		resultBalance:                  input.ResultBalance,
		referenceAttempts:              input.ReferenceAttempts,
		nextReferenceAt:                input.NextReferenceAt,
		createdAt:                      input.CreatedAt.UTC(),
		updatedAt:                      input.UpdatedAt.UTC(),
		processedAt:                    input.ProcessedAt,
	}, nil
}

func (t *Transaction) MarkProcessed(resultBalance money.Money, now time.Time) error {
	if t.status != StatusPending && t.status != StatusPendingReference {
		return ErrInvalidTransition
	}
	if !resultBalance.IsValid() || resultBalance.IsNegative() {
		return fmt.Errorf("%w: invalid result balance", ErrInvalidTransaction)
	}
	if resultBalance.Currency() != t.amount.Currency() {
		return fmt.Errorf("%w: result balance currency mismatch", ErrInvalidTransaction)
	}
	if now.IsZero() {
		return fmt.Errorf("%w: processed time is required", ErrInvalidTransaction)
	}

	now = now.UTC()
	balance := resultBalance
	t.status = StatusProcessed
	t.resultBalance = &balance
	t.processedAt = &now
	t.updatedAt = now
	t.nextReferenceAt = nil
	return nil
}

func (t *Transaction) MarkRejected(failureCode string, now time.Time) error {
	if t.status != StatusPending && t.status != StatusPendingReference {
		return ErrInvalidTransition
	}
	if failureCode == "" {
		return fmt.Errorf("%w: failure code is required", ErrInvalidTransaction)
	}
	if now.IsZero() {
		return fmt.Errorf("%w: processed time is required", ErrInvalidTransaction)
	}

	now = now.UTC()
	t.status = StatusRejected
	t.failureCode = stringPointer(failureCode)
	t.processedAt = &now
	t.updatedAt = now
	t.resultBalance = nil
	t.nextReferenceAt = nil
	return nil
}

func (t *Transaction) MarkFailed(failureCode string, now time.Time) error {
	if t.status != StatusPending && t.status != StatusPendingReference {
		return ErrInvalidTransition
	}
	if failureCode == "" {
		return fmt.Errorf("%w: failure code is required", ErrInvalidTransaction)
	}
	if now.IsZero() {
		return fmt.Errorf("%w: processed time is required", ErrInvalidTransaction)
	}

	now = now.UTC()
	t.status = StatusFailed
	t.failureCode = stringPointer(failureCode)
	t.processedAt = &now
	t.updatedAt = now
	t.resultBalance = nil
	t.nextReferenceAt = nil
	return nil
}

func (t *Transaction) MarkPendingReference(nextAttemptAt, now time.Time) error {
	if t.status != StatusPending {
		return ErrInvalidTransition
	}
	if nextAttemptAt.IsZero() || now.IsZero() {
		return fmt.Errorf("%w: schedule is required", ErrInvalidTransaction)
	}

	t.status = StatusPendingReference
	t.nextReferenceAt = timePointer(nextAttemptAt.UTC())
	t.referenceAttempts++
	t.updatedAt = now.UTC()
	return nil
}

func (t *Transaction) ReschedulePendingReference(nextAttemptAt, now time.Time) error {
	if t.status != StatusPendingReference {
		return ErrInvalidTransition
	}
	if nextAttemptAt.IsZero() || now.IsZero() {
		return fmt.Errorf("%w: schedule is required", ErrInvalidTransaction)
	}

	t.nextReferenceAt = timePointer(nextAttemptAt.UTC())
	t.referenceAttempts++
	t.updatedAt = now.UTC()
	return nil
}

func (t *Transaction) SetReferenceTransactionID(id uuid.UUID) error {
	if id == uuid.Nil {
		return fmt.Errorf("%w: reference id is required", ErrInvalidTransaction)
	}
	if t.status != StatusPending && t.status != StatusPendingReference {
		return ErrInvalidTransition
	}
	t.referenceTransactionID = &id
	return nil
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

func stringPointerIfNotEmpty(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func timePointer(value time.Time) *time.Time {
	return &value
}

func validOrigin(origin Origin) bool {
	return origin == OriginInternal || origin == OriginExternal
}

func validKind(kind Kind) bool {
	switch kind {
	case KindOpening, KindBet, KindWin, KindLoss, KindRefund, KindRollback:
		return true
	default:
		return false
	}
}

func validStatus(status Status) bool {
	switch status {
	case StatusPending, StatusPendingReference, StatusProcessed, StatusRejected, StatusFailed:
		return true
	default:
		return false
	}
}

func isTerminal(status Status) bool {
	return status == StatusProcessed || status == StatusRejected || status == StatusFailed
}
