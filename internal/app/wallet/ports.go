package wallet

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/TheProzin/Jungle-Go/internal/domain/events"
	"github.com/TheProzin/Jungle-Go/internal/domain/ledger"
	"github.com/TheProzin/Jungle-Go/internal/domain/wagering"
	domainwallet "github.com/TheProzin/Jungle-Go/internal/domain/wallet"
)

type Cursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

type WalletRepository interface {
	Create(ctx context.Context, wallet *domainwallet.Wallet) error
	GetByID(ctx context.Context, id uuid.UUID) (*domainwallet.Wallet, error)
}

type TransactionRepository interface {
	CreateOpening(ctx context.Context, transaction *wagering.Transaction) error
}

type LedgerRepository interface {
	Create(ctx context.Context, entry *ledger.Entry) error
	ListByWallet(ctx context.Context, walletID uuid.UUID, after *Cursor, limit int) ([]*ledger.Entry, error)
	SumByWallet(ctx context.Context, walletID uuid.UUID) (amountMinor int64, entries int, err error)
}

type OutboxRepository interface {
	Insert(ctx context.Context, event events.Event) error
}

type Repositories struct {
	Wallets      WalletRepository
	Transactions TransactionRepository
	Ledger       LedgerRepository
	Outbox       OutboxRepository
}

type UnitOfWork interface {
	Do(ctx context.Context, fn func(Repositories) error) error
	DoRead(ctx context.Context, fn func(Repositories) error) error
}
