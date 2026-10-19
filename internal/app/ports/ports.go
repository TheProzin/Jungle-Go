package ports

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
	GetByIDForUpdate(ctx context.Context, id uuid.UUID) (*domainwallet.Wallet, error)
	UpdateBalance(ctx context.Context, wallet *domainwallet.Wallet, expectedVersion int64) error
}

type TransactionRepository interface {
	Create(ctx context.Context, transaction *wagering.Transaction) error
	Update(ctx context.Context, transaction *wagering.Transaction) error
	FindByID(ctx context.Context, id uuid.UUID) (*wagering.Transaction, error)
	FindByIDForUpdate(ctx context.Context, id uuid.UUID) (*wagering.Transaction, error)
	FindByProviderAndIdempotencyKey(ctx context.Context, providerID, idempotencyKey string) (*wagering.Transaction, error)
	FindByProviderAndExternalID(ctx context.Context, providerID, externalTransactionID string) (*wagering.Transaction, error)
	FindByIdentity(ctx context.Context, providerID, idempotencyKey, externalTransactionID string) ([]*wagering.Transaction, error)
	FindNextPendingReferenceForUpdate(ctx context.Context) (*wagering.Transaction, error)
	ExistsSuccessfulReversal(ctx context.Context, referenceTransactionID uuid.UUID) (bool, error)
}

type LedgerRepository interface {
	Create(ctx context.Context, entry *ledger.Entry) error
	ListByWallet(ctx context.Context, walletID uuid.UUID, after *Cursor, limit int) ([]*ledger.Entry, error)
	SumByWallet(ctx context.Context, walletID uuid.UUID) (amountMinor int64, entries int, err error)
}

type OutboxEvent struct {
	ID            uuid.UUID
	AggregateType string
	AggregateID   string
	EventType     string
	EventVersion  int
	Payload       []byte
	CorrelationID string
	CausationID   *string
	OccurredAt    time.Time
	Attempts      int
}

type OutboxRepository interface {
	Insert(ctx context.Context, event events.Event) error
	ClaimBatch(ctx context.Context, limit int, lease time.Duration) ([]OutboxEvent, error)
	MarkPublished(ctx context.Context, eventID uuid.UUID, publishedAt time.Time) error
	MarkFailed(ctx context.Context, eventID uuid.UUID, nextAttemptAt time.Time, lastError string) error
}

type InboxRepository interface {
	Insert(ctx context.Context, consumerName, messageID, payloadHash string, receivedAt time.Time) (inserted bool, existingHash string, err error)
	Complete(ctx context.Context, consumerName, messageID string, completedAt time.Time) error
	MarkFailed(ctx context.Context, consumerName, messageID, reason string, completedAt time.Time) error
}

type Repositories struct {
	Wallets      WalletRepository
	Transactions TransactionRepository
	Ledger       LedgerRepository
	Outbox       OutboxRepository
	Inbox        InboxRepository
}

type UnitOfWork interface {
	Do(ctx context.Context, fn func(Repositories) error) error
	DoRead(ctx context.Context, fn func(Repositories) error) error
}
