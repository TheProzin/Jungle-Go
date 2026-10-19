package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/TheProzin/Jungle-Go/internal/app/ports"
	"github.com/TheProzin/Jungle-Go/internal/domain/ledger"
	"github.com/TheProzin/Jungle-Go/internal/domain/money"
)

type LedgerRepository struct {
	query querier
}

func (r *LedgerRepository) Create(ctx context.Context, entry *ledger.Entry) error {
	_, err := r.query.Exec(ctx, `
		INSERT INTO wallet_ledger_entries (
			id, wallet_id, transaction_id, direction, amount_minor, currency,
			balance_before_minor, balance_after_minor, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`,
		entry.ID().String(),
		entry.WalletID().String(),
		entry.TransactionID().String(),
		string(entry.Direction()),
		entry.Amount().Minor(),
		entry.Amount().Currency(),
		entry.BalanceBefore().Minor(),
		entry.BalanceAfter().Minor(),
		entry.CreatedAt(),
	)
	if err != nil {
		return fmt.Errorf("insert ledger entry: %w", err)
	}
	return nil
}

func (r *LedgerRepository) ListByWallet(
	ctx context.Context,
	walletID uuid.UUID,
	after *ports.Cursor,
	limit int,
) ([]*ledger.Entry, error) {
	query := `
		SELECT id, wallet_id, transaction_id, direction, amount_minor, currency,
		       balance_before_minor, balance_after_minor, created_at
		FROM wallet_ledger_entries
		WHERE wallet_id = $1
	`
	args := []any{walletID.String()}

	if after != nil {
		query += ` AND (created_at, id) > ($2::timestamptz, $3::uuid)`
		args = append(args, after.CreatedAt, after.ID.String())
	}

	query += fmt.Sprintf(` ORDER BY created_at, id LIMIT $%d`, len(args)+1)
	args = append(args, limit)

	rows, err := r.query.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query ledger: %w", err)
	}
	defer rows.Close()

	entries := make([]*ledger.Entry, 0, limit)
	for rows.Next() {
		var (
			idText        string
			walletIDText  string
			transactionID string
			direction     string
			amountMinor   int64
			currency      string
			beforeMinor   int64
			afterMinor    int64
			createdAt     time.Time
		)
		if err := rows.Scan(
			&idText,
			&walletIDText,
			&transactionID,
			&direction,
			&amountMinor,
			&currency,
			&beforeMinor,
			&afterMinor,
			&createdAt,
		); err != nil {
			return nil, fmt.Errorf("scan ledger entry: %w", err)
		}

		entryID, err := uuid.Parse(idText)
		if err != nil {
			return nil, fmt.Errorf("parse ledger id: %w", err)
		}
		entryWalletID, err := uuid.Parse(walletIDText)
		if err != nil {
			return nil, fmt.Errorf("parse ledger wallet id: %w", err)
		}
		transactionIDParsed, err := uuid.Parse(transactionID)
		if err != nil {
			return nil, fmt.Errorf("parse ledger transaction id: %w", err)
		}

		amount, err := money.New(amountMinor, strings.TrimSpace(currency))
		if err != nil {
			return nil, fmt.Errorf("parse ledger amount: %w", err)
		}
		before, err := money.New(beforeMinor, strings.TrimSpace(currency))
		if err != nil {
			return nil, fmt.Errorf("parse ledger balance: %w", err)
		}
		afterBalance, err := money.New(afterMinor, strings.TrimSpace(currency))
		if err != nil {
			return nil, fmt.Errorf("parse ledger balance: %w", err)
		}

		entry, err := ledger.Rehydrate(
			entryID,
			entryWalletID,
			transactionIDParsed,
			ledger.Direction(direction),
			amount,
			before,
			afterBalance,
			createdAt,
		)
		if err != nil {
			return nil, fmt.Errorf("rehydrate ledger entry: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate ledger: %w", err)
	}
	return entries, nil
}

func (r *LedgerRepository) SumByWallet(ctx context.Context, walletID uuid.UUID) (int64, int, error) {
	var (
		sumMinor int64
		entries  int64
	)
	err := r.query.QueryRow(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN direction = 'CREDIT' THEN amount_minor ELSE -amount_minor END), 0)::bigint,
			COUNT(*)::bigint
		FROM wallet_ledger_entries
		WHERE wallet_id = $1
	`, walletID.String()).Scan(&sumMinor, &entries)
	if err != nil {
		return 0, 0, fmt.Errorf("sum ledger: %w", err)
	}
	return sumMinor, int(entries), nil
}
