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
	var (
		idText       string
		playerIDText string
		currency     string
		balanceMinor int64
		version      int64
		createdAt    time.Time
		updatedAt    time.Time
	)

	err := r.query.QueryRow(ctx, `
		SELECT id, player_id, currency, balance_minor, version, created_at, updated_at
		FROM wallets
		WHERE id = $1
	`, id.String()).Scan(
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
