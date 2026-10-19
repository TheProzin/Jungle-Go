package wallet

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/TheProzin/Jungle-Go/internal/domain/events"
	"github.com/TheProzin/Jungle-Go/internal/domain/ledger"
	"github.com/TheProzin/Jungle-Go/internal/domain/money"
	"github.com/TheProzin/Jungle-Go/internal/domain/wagering"
	domainwallet "github.com/TheProzin/Jungle-Go/internal/domain/wallet"
)

var ErrInvalidCursor = errors.New("invalid cursor")

type Service struct {
	uow   UnitOfWork
	newID func() (uuid.UUID, error)
	now   func() time.Time
}

type CreateWalletCommand struct {
	PlayerID       uuid.UUID
	InitialBalance money.Money
	CorrelationID  string
}

type LedgerPage struct {
	Entries    []*ledger.Entry
	NextCursor string
}

type Reconciliation struct {
	WalletID          uuid.UUID
	StoredBalance     money.Money
	CalculatedBalance money.Money
	Difference        money.Money
	Consistent        bool
	CheckedEntries    int
}

func NewService(uow UnitOfWork) *Service {
	return &Service{
		uow:   uow,
		newID: uuid.NewV7,
		now:   func() time.Time { return time.Now().UTC() },
	}
}

func (s *Service) CreateWallet(ctx context.Context, cmd CreateWalletCommand) (*domainwallet.Wallet, error) {
	if cmd.CorrelationID == "" {
		cmd.CorrelationID = uuid.NewString()
	}

	now := s.now()
	walletID, err := s.newID()
	if err != nil {
		return nil, fmt.Errorf("generate wallet id: %w", err)
	}

	created, err := domainwallet.New(walletID, cmd.PlayerID, cmd.InitialBalance, now)
	if err != nil {
		return nil, err
	}

	err = s.uow.Do(ctx, func(repos Repositories) error {
		if err := repos.Wallets.Create(ctx, created); err != nil {
			return err
		}
		if !cmd.InitialBalance.IsPositive() {
			return nil
		}
		return s.createOpeningRecords(ctx, repos, created, cmd.CorrelationID, now)
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

func (s *Service) createOpeningRecords(
	ctx context.Context,
	repos Repositories,
	created *domainwallet.Wallet,
	correlationID string,
	now time.Time,
) error {
	openingID, err := s.newID()
	if err != nil {
		return fmt.Errorf("generate opening id: %w", err)
	}

	opening, err := wagering.NewOpening(
		openingID,
		"opening:"+created.ID().String(),
		created.ID(),
		created.PlayerID(),
		created.Balance(),
		now,
	)
	if err != nil {
		return err
	}
	if err := repos.Transactions.CreateOpening(ctx, opening); err != nil {
		return err
	}

	zero, err := money.Zero(created.Balance().Currency())
	if err != nil {
		return err
	}

	ledgerID, err := s.newID()
	if err != nil {
		return fmt.Errorf("generate ledger id: %w", err)
	}
	entry, err := ledger.NewCredit(
		ledgerID,
		created.ID(),
		opening.ID(),
		created.Balance(),
		zero,
		created.Balance(),
		now,
	)
	if err != nil {
		return err
	}
	if err := repos.Ledger.Create(ctx, entry); err != nil {
		return err
	}

	processedEventID, err := s.newID()
	if err != nil {
		return fmt.Errorf("generate processed event id: %w", err)
	}
	processedEvent, err := events.NewWagerTransactionProcessed(
		processedEventID,
		opening.ID().String(),
		correlationID,
		nil,
		now,
		events.WagerTransactionProcessedData{
			TransactionID: opening.ID().String(),
			Kind:          string(opening.Kind()),
			WalletID:      created.ID().String(),
			PlayerID:      created.PlayerID().String(),
			Money:         created.Balance(),
			Status:        string(opening.Status()),
		},
	)
	if err != nil {
		return err
	}
	if err := repos.Outbox.Insert(ctx, processedEvent); err != nil {
		return err
	}

	balanceEventID, err := s.newID()
	if err != nil {
		return fmt.Errorf("generate balance event id: %w", err)
	}
	causationID := opening.ID().String()
	balanceEvent, err := events.NewWalletBalanceChanged(
		balanceEventID,
		created.ID().String(),
		correlationID,
		&causationID,
		now,
		events.WalletBalanceChangedData{
			WalletID:      created.ID().String(),
			TransactionID: opening.ID().String(),
			Direction:     ledger.DirectionCredit,
			Money:         created.Balance(),
			BalanceBefore: zero,
			BalanceAfter:  created.Balance(),
			WalletVersion: created.Version(),
		},
	)
	if err != nil {
		return err
	}
	return repos.Outbox.Insert(ctx, balanceEvent)
}

func (s *Service) GetWallet(ctx context.Context, walletID uuid.UUID) (*domainwallet.Wallet, error) {
	var result *domainwallet.Wallet
	err := s.uow.DoRead(ctx, func(repos Repositories) error {
		found, err := repos.Wallets.GetByID(ctx, walletID)
		if err != nil {
			return err
		}
		result = found
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) GetLedger(ctx context.Context, walletID uuid.UUID, cursor string, limit int) (*LedgerPage, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	var after *Cursor
	if cursor != "" {
		decoded, err := decodeCursor(cursor)
		if err != nil {
			return nil, err
		}
		after = &decoded
	}

	page := &LedgerPage{}
	err := s.uow.DoRead(ctx, func(repos Repositories) error {
		if _, err := repos.Wallets.GetByID(ctx, walletID); err != nil {
			return err
		}

		entries, err := repos.Ledger.ListByWallet(ctx, walletID, after, limit+1)
		if err != nil {
			return err
		}
		if len(entries) > limit {
			page.NextCursor = encodeCursor(entries[limit-1].CreatedAt(), entries[limit-1].ID())
			entries = entries[:limit]
		}
		page.Entries = entries
		return nil
	})
	if err != nil {
		return nil, err
	}
	return page, nil
}

func (s *Service) Reconcile(ctx context.Context, walletID uuid.UUID) (*Reconciliation, error) {
	var result *Reconciliation
	err := s.uow.DoRead(ctx, func(repos Repositories) error {
		found, err := repos.Wallets.GetByID(ctx, walletID)
		if err != nil {
			return err
		}
		sumMinor, entries, err := repos.Ledger.SumByWallet(ctx, walletID)
		if err != nil {
			return err
		}
		calculated, err := money.New(sumMinor, found.Balance().Currency())
		if err != nil {
			return err
		}
		difference, err := found.Balance().Sub(calculated)
		if err != nil {
			return err
		}
		result = &Reconciliation{
			WalletID:          found.ID(),
			StoredBalance:     found.Balance(),
			CalculatedBalance: calculated,
			Difference:        difference,
			Consistent:        difference.IsZero(),
			CheckedEntries:    entries,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func encodeCursor(createdAt time.Time, id uuid.UUID) string {
	raw := createdAt.UTC().Format(time.RFC3339Nano) + "|" + id.String()
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(cursor string) (Cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return Cursor{}, fmt.Errorf("%w: %v", ErrInvalidCursor, err)
	}

	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return Cursor{}, ErrInvalidCursor
	}

	createdAt, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return Cursor{}, fmt.Errorf("%w: %v", ErrInvalidCursor, err)
	}
	id, err := uuid.Parse(parts[1])
	if err != nil {
		return Cursor{}, fmt.Errorf("%w: %v", ErrInvalidCursor, err)
	}

	return Cursor{CreatedAt: createdAt.UTC(), ID: id}, nil
}
