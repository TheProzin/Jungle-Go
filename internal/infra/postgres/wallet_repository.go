package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/TheProzin/Jungle-Go/internal/domain/money"
	domainwallet "github.com/TheProzin/Jungle-Go/internal/domain/wallet"
)

type WalletRepository struct {
	query querier
}

func (r *WalletRepository) Create(ctx context.Context, wallet *domainwallet.Wallet) error {
	_, err := r.query.Exec(ctx, `
		INSERT INTO wallets (id, player_id, currency, balance_minor, version, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`,
		wallet.ID().String(),
		wallet.PlayerID().String(),
		wallet.Balance().Currency(),
		wallet.Balance().Minor(),
		wallet.Version(),
		wallet.CreatedAt(),
		wallet.UpdatedAt(),
	)
	return mapWalletWriteError(err)
}

func (r *WalletRepository) GetByID(ctx context.Context, id uuid.UUID) (*domainwallet.Wallet, error) {
	return r.getByID(ctx, id, false)
}

func (r *WalletRepository) GetByIDForUpdate(ctx context.Context, id uuid.UUID) (*domainwallet.Wallet, error) {
	return r.getByID(ctx, id, true)
}

func (r *WalletRepository) UpdateBalance(ctx context.Context, wallet *domainwallet.Wallet, expectedVersion int64) error {
	tag, err := r.query.Exec(ctx, `
		UPDATE wallets
		SET balance_minor = $1, version = $2, updated_at = $3
		WHERE id = $4 AND version = $5
	`,
		wallet.Balance().Minor(),
		wallet.Version(),
		wallet.UpdatedAt(),
		wallet.ID().String(),
		expectedVersion,
	)
	if err != nil {
		return fmt.Errorf("update wallet balance: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domainwallet.ErrConcurrentUpdate
	}
	return nil
}

func (r *WalletRepository) getByID(ctx context.Context, id uuid.UUID, forUpdate bool) (*domainwallet.Wallet, error) {
	var (
		idText       string
		playerIDText string
		currency     string
		balanceMinor int64
		version      int64
		createdAt    time.Time
		updatedAt    time.Time
	)

	query := `
		SELECT id, player_id, currency, balance_minor, version, created_at, updated_at
		FROM wallets
		WHERE id = $1
	`
	if forUpdate {
		query += ` FOR UPDATE`
	}

	err := r.query.QueryRow(ctx, query, id.String()).Scan(
		&idText,
		&playerIDText,
		&currency,
		&balanceMinor,
		&version,
		&createdAt,
		&updatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domainwallet.ErrWalletNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query wallet: %w", err)
	}

	walletID, err := uuid.Parse(idText)
	if err != nil {
		return nil, fmt.Errorf("parse wallet id: %w", err)
	}
	playerID, err := uuid.Parse(playerIDText)
	if err != nil {
		return nil, fmt.Errorf("parse player id: %w", err)
	}
	balance, err := money.New(balanceMinor, strings.TrimSpace(currency))
	if err != nil {
		return nil, fmt.Errorf("parse wallet balance: %w", err)
	}

	return domainwallet.Rehydrate(walletID, playerID, balance, version, createdAt, updatedAt)
}

func mapWalletWriteError(err error) error {
	if err == nil {
		return nil
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "wallets_player_currency_unique" {
		return domainwallet.ErrWalletAlreadyExists
	}
	return fmt.Errorf("write wallet: %w", err)
}
