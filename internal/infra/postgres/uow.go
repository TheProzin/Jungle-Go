package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	walletapp "github.com/TheProzin/Jungle-Go/internal/app/wallet"
)

type querier interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type UnitOfWork struct {
	pool *pgxpool.Pool
}

func NewUnitOfWork(pool *pgxpool.Pool) walletapp.UnitOfWork {
	return &UnitOfWork{pool: pool}
}

func (u *UnitOfWork) Do(ctx context.Context, fn func(walletapp.Repositories) error) error {
	return u.run(ctx, pgx.TxOptions{}, fn)
}

func (u *UnitOfWork) DoRead(ctx context.Context, fn func(walletapp.Repositories) error) error {
	return u.run(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	}, fn)
}

func (u *UnitOfWork) run(ctx context.Context, options pgx.TxOptions, fn func(walletapp.Repositories) error) error {
	tx, err := u.pool.BeginTx(ctx, options)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	repositories := walletapp.Repositories{
		Wallets:      &WalletRepository{query: tx},
		Transactions: &TransactionRepository{query: tx},
		Ledger:       &LedgerRepository{query: tx},
		Outbox:       &OutboxRepository{query: tx},
	}

	if err := fn(repositories); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}
