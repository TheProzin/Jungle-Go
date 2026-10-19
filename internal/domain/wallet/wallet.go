package wallet

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TheProzin/Jungle-Go/internal/domain/money"
)

var (
	ErrInvalidWallet          = errors.New("invalid wallet")
	ErrNegativeInitialBalance = errors.New("negative initial balance")
	ErrInsufficientFunds      = errors.New("insufficient funds")
	ErrNonPositiveAmount      = errors.New("non-positive amount")
	ErrWalletNotFound         = errors.New("wallet not found")
	ErrWalletAlreadyExists    = errors.New("wallet already exists")
	ErrConcurrentUpdate       = errors.New("concurrent wallet update")
)

type Wallet struct {
	id        uuid.UUID
	playerID  uuid.UUID
	balance   money.Money
	version   int64
	createdAt time.Time
	updatedAt time.Time
}

func New(id, playerID uuid.UUID, initialBalance money.Money, now time.Time) (*Wallet, error) {
	if id == uuid.Nil || playerID == uuid.Nil {
		return nil, fmt.Errorf("%w: id and player id are required", ErrInvalidWallet)
	}
	if !initialBalance.IsValid() {
		return nil, fmt.Errorf("%w: invalid initial balance", ErrInvalidWallet)
	}
	if initialBalance.IsNegative() {
		return nil, ErrNegativeInitialBalance
	}
	if now.IsZero() {
		return nil, fmt.Errorf("%w: creation time is required", ErrInvalidWallet)
	}

	now = now.UTC()
	return &Wallet{
		id:        id,
		playerID:  playerID,
		balance:   initialBalance,
		version:   1,
		createdAt: now,
		updatedAt: now,
	}, nil
}

func Rehydrate(
	id, playerID uuid.UUID,
	balance money.Money,
	version int64,
	createdAt, updatedAt time.Time,
) (*Wallet, error) {
	if id == uuid.Nil || playerID == uuid.Nil {
		return nil, fmt.Errorf("%w: id and player id are required", ErrInvalidWallet)
	}
	if !balance.IsValid() || balance.IsNegative() {
		return nil, fmt.Errorf("%w: invalid balance", ErrInvalidWallet)
	}
	if version < 1 {
		return nil, fmt.Errorf("%w: version must be positive", ErrInvalidWallet)
	}
	if createdAt.IsZero() || updatedAt.IsZero() {
		return nil, fmt.Errorf("%w: timestamps are required", ErrInvalidWallet)
	}

	return &Wallet{
		id:        id,
		playerID:  playerID,
		balance:   balance,
		version:   version,
		createdAt: createdAt.UTC(),
		updatedAt: updatedAt.UTC(),
	}, nil
}

func (w *Wallet) Debit(amount money.Money, now time.Time) error {
	if now.IsZero() {
		return fmt.Errorf("%w: update time is required", ErrInvalidWallet)
	}
	if !amount.IsPositive() {
		return ErrNonPositiveAmount
	}
	if amount.Currency() != w.balance.Currency() {
		return fmt.Errorf("%w: wallet %s, amount %s", money.ErrCurrencyMismatch, w.balance.Currency(), amount.Currency())
	}
	if w.balance.Minor() < amount.Minor() {
		return ErrInsufficientFunds
	}

	updated, err := w.balance.Sub(amount)
	if err != nil {
		return err
	}

	w.balance = updated
	w.version++
	w.updatedAt = now.UTC()
	return nil
}

func (w *Wallet) Credit(amount money.Money, now time.Time) error {
	if now.IsZero() {
		return fmt.Errorf("%w: update time is required", ErrInvalidWallet)
	}
	if !amount.IsPositive() {
		return ErrNonPositiveAmount
	}
	if amount.Currency() != w.balance.Currency() {
		return fmt.Errorf("%w: wallet %s, amount %s", money.ErrCurrencyMismatch, w.balance.Currency(), amount.Currency())
	}

	updated, err := w.balance.Add(amount)
	if err != nil {
		return err
	}

	w.balance = updated
	w.version++
	w.updatedAt = now.UTC()
	return nil
}

func (w *Wallet) ID() uuid.UUID {
	return w.id
}

func (w *Wallet) PlayerID() uuid.UUID {
	return w.playerID
}

func (w *Wallet) Balance() money.Money {
	return w.balance
}

func (w *Wallet) Version() int64 {
	return w.version
}

func (w *Wallet) CreatedAt() time.Time {
	return w.createdAt
}

func (w *Wallet) UpdatedAt() time.Time {
	return w.updatedAt
}
