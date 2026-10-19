package ledger

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TheProzin/Jungle-Go/internal/domain/money"
)

type Direction string

const (
	DirectionDebit  Direction = "DEBIT"
	DirectionCredit Direction = "CREDIT"
)

var (
	ErrInvalidEntry    = errors.New("invalid ledger entry")
	ErrBalanceMismatch = errors.New("ledger balance mismatch")
)

type Entry struct {
	id            uuid.UUID
	walletID      uuid.UUID
	transactionID uuid.UUID
	direction     Direction
	amount        money.Money
	balanceBefore money.Money
	balanceAfter  money.Money
	createdAt     time.Time
}

func NewCredit(
	id, walletID, transactionID uuid.UUID,
	amount, balanceBefore, balanceAfter money.Money,
	createdAt time.Time,
) (*Entry, error) {
	return newEntry(id, walletID, transactionID, DirectionCredit, amount, balanceBefore, balanceAfter, createdAt)
}

func NewDebit(
	id, walletID, transactionID uuid.UUID,
	amount, balanceBefore, balanceAfter money.Money,
	createdAt time.Time,
) (*Entry, error) {
	return newEntry(id, walletID, transactionID, DirectionDebit, amount, balanceBefore, balanceAfter, createdAt)
}

func Rehydrate(
	id, walletID, transactionID uuid.UUID,
	direction Direction,
	amount, balanceBefore, balanceAfter money.Money,
	createdAt time.Time,
) (*Entry, error) {
	return newEntry(id, walletID, transactionID, direction, amount, balanceBefore, balanceAfter, createdAt)
}

func newEntry(
	id, walletID, transactionID uuid.UUID,
	direction Direction,
	amount, balanceBefore, balanceAfter money.Money,
	createdAt time.Time,
) (*Entry, error) {
	if id == uuid.Nil || walletID == uuid.Nil || transactionID == uuid.Nil {
		return nil, fmt.Errorf("%w: identifiers are required", ErrInvalidEntry)
	}
	if direction != DirectionDebit && direction != DirectionCredit {
		return nil, fmt.Errorf("%w: unknown direction %q", ErrInvalidEntry, direction)
	}
	if !amount.IsValid() || !amount.IsPositive() {
		return nil, fmt.Errorf("%w: amount must be positive and valid", ErrInvalidEntry)
	}
	if !balanceBefore.IsValid() || !balanceAfter.IsValid() {
		return nil, fmt.Errorf("%w: balances must be valid", ErrInvalidEntry)
	}
	if balanceBefore.IsNegative() || balanceAfter.IsNegative() {
		return nil, fmt.Errorf("%w: balances cannot be negative", ErrInvalidEntry)
	}
	if amount.Currency() != balanceBefore.Currency() ||
		amount.Currency() != balanceAfter.Currency() {
		return nil, fmt.Errorf("%w: currencies must match", ErrInvalidEntry)
	}
	if createdAt.IsZero() {
		return nil, fmt.Errorf("%w: creation time is required", ErrInvalidEntry)
	}

	var expected money.Money
	var err error
	switch direction {
	case DirectionCredit:
		expected, err = balanceBefore.Add(amount)
	case DirectionDebit:
		if balanceBefore.Minor() < amount.Minor() {
			return nil, ErrBalanceMismatch
		}
		expected, err = balanceBefore.Sub(amount)
	}
	if err != nil {
		return nil, err
	}
	if !expected.Equal(balanceAfter) {
		return nil, ErrBalanceMismatch
	}

	return &Entry{
		id:            id,
		walletID:      walletID,
		transactionID: transactionID,
		direction:     direction,
		amount:        amount,
		balanceBefore: balanceBefore,
		balanceAfter:  balanceAfter,
		createdAt:     createdAt.UTC(),
	}, nil
}

func (e *Entry) ID() uuid.UUID {
	return e.id
}

func (e *Entry) WalletID() uuid.UUID {
	return e.walletID
}

func (e *Entry) TransactionID() uuid.UUID {
	return e.transactionID
}

func (e *Entry) Direction() Direction {
	return e.direction
}

func (e *Entry) Amount() money.Money {
	return e.amount
}

func (e *Entry) BalanceBefore() money.Money {
	return e.balanceBefore
}

func (e *Entry) BalanceAfter() money.Money {
	return e.balanceAfter
}

func (e *Entry) CreatedAt() time.Time {
	return e.createdAt
}
