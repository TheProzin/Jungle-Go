//go:build integration

package integration

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/TheProzin/Jungle-Go/internal/infra/migrate"
)

const (
	walletID      = "11111111-1111-7111-8111-111111111111"
	secondWallet  = "22222222-2222-7222-8222-222222222222"
	playerID      = "33333333-3333-7333-8333-333333333333"
	secondPlayer  = "44444444-4444-7444-8444-444444444444"
	betID         = "55555555-5555-7555-8555-555555555555"
	openingID     = "66666666-6666-7666-8666-666666666666"
	refundOneID   = "77777777-7777-7777-8777-777777777777"
	refundTwoID   = "88888888-8888-7888-8888-888888888888"
	pendingBetID  = "99999999-9999-7999-8999-999999999999"
	ledgerID      = "aaaaaaaa-aaaa-7aaa-8aaa-aaaaaaaaaaaa"
	secondLedger  = "bbbbbbbb-bbbb-7bbb-8bbb-bbbbbbbbbbbb"
	outboxID      = "dddddddd-dddd-7ddd-8ddd-dddddddddddd"
	missingTXID   = "eeeeeeee-eeee-7eee-8eee-eeeeeeeeeeee"
	betExternalID = "bet-external-1"
)

func TestMigrationsUpDown(t *testing.T) {
	_, dsn := newTestDatabase(t, false)

	if err := migrate.Up(dsn); err != nil {
		t.Fatalf("Up() error = %v", err)
	}
	assertVersion(t, dsn, 2)

	if err := migrate.Down(dsn, 1); err != nil {
		t.Fatalf("Down(1) error = %v", err)
	}
	assertVersion(t, dsn, 1)

	if err := migrate.Up(dsn); err != nil {
		t.Fatalf("Up() error = %v", err)
	}
	assertVersion(t, dsn, 2)

	if err := migrate.Down(dsn, 2); err != nil {
		t.Fatalf("Down(2) error = %v", err)
	}
	assertVersion(t, dsn, 0)
}

func TestSchemaRejectsDuplicates(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	insertWallet(t, ctx, pool, walletID, playerID, "BRL", 10000)
	insertWallet(t, ctx, pool, secondWallet, secondPlayer, "BRL", 10000)
	insertProcessedBet(t, ctx, pool, betID, betExternalID, "idem-bet-1", walletID, playerID, 1000)

	mustFail(t, ctx, pool, `
		INSERT INTO wallets (id, player_id, currency, balance_minor, version, created_at, updated_at)
		VALUES ('12121212-1212-7212-8212-121212121212', $1, 'BRL', 0, 1, now(), now())
	`, playerID)

	mustFail(t, ctx, pool, `
		INSERT INTO wager_transactions (
			id, origin, kind, status, provider_id, external_transaction_id, idempotency_key,
			payload_hash, wallet_id, player_id, round_id, game_id, amount_minor, currency,
			result_balance_minor, result_currency, created_at, updated_at, processed_at
		) VALUES (
			'13131313-1313-7313-8313-131313131313', 'EXTERNAL', 'BET', 'PROCESSED', 'provider-a',
			$1, 'idem-other', 'hash', $2, $3, 'round-1', 'game-1', 100, 'BRL', 9900, 'BRL',
			now(), now(), now()
		)
	`, betExternalID, walletID, playerID)

	mustFail(t, ctx, pool, `
		INSERT INTO wager_transactions (
			id, origin, kind, status, provider_id, external_transaction_id, idempotency_key,
			payload_hash, wallet_id, player_id, round_id, game_id, amount_minor, currency,
			result_balance_minor, result_currency, created_at, updated_at, processed_at
		) VALUES (
			'14141414-1414-7414-8414-141414141414', 'EXTERNAL', 'BET', 'PROCESSED', 'provider-a',
			'other-external', 'idem-bet-1', 'hash', $1, $2, 'round-1', 'game-1', 100, 'BRL',
			9900, 'BRL', now(), now(), now()
		)
	`, walletID, playerID)

	insertOpening(t, ctx, pool, openingID, walletID, playerID, 10000, "opening-1")

	mustFail(t, ctx, pool, `
		INSERT INTO wager_transactions (
			id, origin, kind, status, provider_id, internal_reference, wallet_id, player_id,
			amount_minor, currency, result_balance_minor, result_currency, created_at, updated_at, processed_at
		) VALUES (
			'15151515-1515-7515-8515-151515151515', 'INTERNAL', 'OPENING', 'PROCESSED', NULL,
			'opening-2', $1, $2, 100, 'BRL', 100, 'BRL', now(), now(), now()
		)
	`, walletID, playerID)

	insertReversal(t, ctx, pool, refundOneID, "refund-1", "idem-refund-1", "REFUND", betID, betExternalID, walletID, playerID, 1000)

	mustFail(t, ctx, pool, `
		INSERT INTO wager_transactions (
			id, origin, kind, status, provider_id, external_transaction_id, idempotency_key,
			payload_hash, wallet_id, player_id, round_id, game_id, amount_minor, currency,
			reference_external_transaction_id, reference_transaction_id, result_balance_minor,
			result_currency, created_at, updated_at, processed_at
		) VALUES (
			$1, 'EXTERNAL', 'ROLLBACK', 'PROCESSED', 'provider-a', 'rollback-2', 'idem-rollback-2',
			'hash', $2, $3, 'round-1', 'game-1', 1000, 'BRL', $4, $5, 9100, 'BRL',
			now(), now(), now()
		)
	`, refundTwoID, walletID, playerID, betExternalID, betID)

	mustExec(t, ctx, pool, `
		INSERT INTO inbox_messages (consumer_name, message_id, payload_hash, status, received_at)
		VALUES ('consumer', 'message-1', 'hash', 'RECEIVED', now())
	`)
	mustFail(t, ctx, pool, `
		INSERT INTO inbox_messages (consumer_name, message_id, payload_hash, status, received_at)
		VALUES ('consumer', 'message-1', 'other-hash', 'RECEIVED', now())
	`)
}

func TestSchemaRejectsInvalidFinancialRows(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	insertWallet(t, ctx, pool, walletID, playerID, "BRL", 10000)

	tests := []struct {
		name string
		sql  string
		args []any
	}{
		{
			name: "negative wallet balance",
			sql: `
				INSERT INTO wallets (id, player_id, currency, balance_minor, version, created_at, updated_at)
				VALUES ('16161616-1616-7616-8616-161616161616', $1, 'BRL', -1, 1, now(), now())
			`,
			args: []any{secondPlayer},
		},
		{
			name: "loss with non zero amount",
			sql: `
				INSERT INTO wager_transactions (
					id, origin, kind, status, provider_id, external_transaction_id, idempotency_key,
					payload_hash, wallet_id, player_id, round_id, game_id, amount_minor, currency,
					result_balance_minor, result_currency, created_at, updated_at, processed_at
				) VALUES (
					'17171717-1717-7717-8717-171717171717', 'EXTERNAL', 'LOSS', 'PROCESSED',
					'provider-a', 'loss-1', 'idem-loss-1', 'hash', $1, $2, 'round-1', 'game-1',
					1, 'BRL', 10000, 'BRL', now(), now(), now()
				)
			`,
			args: []any{walletID, playerID},
		},
		{
			name: "external without round",
			sql: `
				INSERT INTO wager_transactions (
					id, origin, kind, status, provider_id, external_transaction_id, idempotency_key,
					payload_hash, wallet_id, player_id, game_id, amount_minor, currency,
					result_balance_minor, result_currency, created_at, updated_at, processed_at
				) VALUES (
					'18181818-1818-7818-8818-181818181818', 'EXTERNAL', 'BET', 'PROCESSED',
					'provider-a', 'bet-no-round', 'idem-no-round', 'hash', $1, $2, 'game-1',
					100, 'BRL', 9900, 'BRL', now(), now(), now()
				)
			`,
			args: []any{walletID, playerID},
		},
		{
			name: "opening marked external",
			sql: `
				INSERT INTO wager_transactions (
					id, origin, kind, status, provider_id, external_transaction_id, idempotency_key,
					payload_hash, wallet_id, player_id, round_id, game_id, amount_minor, currency,
					created_at, updated_at
				) VALUES (
					'19191919-1919-7919-8919-191919191919', 'EXTERNAL', 'OPENING', 'PENDING',
					'provider-a', 'opening-external', 'idem-opening', 'hash', $1, $2, 'round-1',
					'game-1', 100, 'BRL', now(), now()
				)
			`,
			args: []any{walletID, playerID},
		},
		{
			name: "terminal without processed at",
			sql: `
				INSERT INTO wager_transactions (
					id, origin, kind, status, provider_id, external_transaction_id, idempotency_key,
					payload_hash, wallet_id, player_id, round_id, game_id, amount_minor, currency,
					result_balance_minor, result_currency, created_at, updated_at
				) VALUES (
					'20202020-2020-7020-8020-202020202020', 'EXTERNAL', 'BET', 'PROCESSED',
					'provider-a', 'bet-no-processed', 'idem-no-processed', 'hash', $1, $2,
					'round-1', 'game-1', 100, 'BRL', 9900, 'BRL', now(), now()
				)
			`,
			args: []any{walletID, playerID},
		},
		{
			name: "result balance without currency",
			sql: `
				INSERT INTO wager_transactions (
					id, origin, kind, status, provider_id, external_transaction_id, idempotency_key,
					payload_hash, wallet_id, player_id, round_id, game_id, amount_minor, currency,
					result_balance_minor, created_at, updated_at, processed_at
				) VALUES (
					'21212121-2121-7121-8121-212121212121', 'EXTERNAL', 'BET', 'PROCESSED',
					'provider-a', 'bet-result-pair', 'idem-result-pair', 'hash', $1, $2,
					'round-1', 'game-1', 100, 'BRL', 9900, now(), now(), now()
				)
			`,
			args: []any{walletID, playerID},
		},
		{
			name: "refund without reference",
			sql: `
				INSERT INTO wager_transactions (
					id, origin, kind, status, provider_id, external_transaction_id, idempotency_key,
					payload_hash, wallet_id, player_id, round_id, game_id, amount_minor, currency,
					created_at, updated_at
				) VALUES (
					'22222222-2222-7222-8222-222222222223', 'EXTERNAL', 'REFUND', 'PENDING',
					'provider-a', 'refund-no-ref', 'idem-refund-no-ref', 'hash', $1, $2,
					'round-1', 'game-1', 100, 'BRL', now(), now()
				)
			`,
			args: []any{walletID, playerID},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mustFail(t, ctx, pool, test.sql, test.args...)
		})
	}
}

func TestLedgerConstraintsAndTriggers(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	insertWallet(t, ctx, pool, walletID, playerID, "BRL", 9000)
	insertWallet(t, ctx, pool, secondWallet, secondPlayer, "BRL", 5000)
	insertProcessedBet(t, ctx, pool, betID, betExternalID, "idem-bet-ledger", walletID, playerID, 1000)

	mustExec(t, ctx, pool, `
		INSERT INTO wallet_ledger_entries (
			id, wallet_id, transaction_id, direction, amount_minor, currency,
			balance_before_minor, balance_after_minor, created_at
		) VALUES ($1, $2, $3, 'DEBIT', 1000, 'BRL', 10000, 9000, now())
	`, ledgerID, walletID, betID)

	insertPendingBet(t, ctx, pool, pendingBetID, "bet-pending-ledger", "idem-pending-ledger", walletID, playerID, 1000)

	mustFail(t, ctx, pool, `
		INSERT INTO wallet_ledger_entries (
			id, wallet_id, transaction_id, direction, amount_minor, currency,
			balance_before_minor, balance_after_minor, created_at
		) VALUES ($1, $2, $3, 'DEBIT', 1000, 'BRL', 10000, 9500, now())
	`, secondLedger, walletID, pendingBetID)

	mustFail(t, ctx, pool, `
		INSERT INTO wallet_ledger_entries (
			id, wallet_id, transaction_id, direction, amount_minor, currency,
			balance_before_minor, balance_after_minor, created_at
		) VALUES ('23232323-2323-7323-8323-232323232323', $1, $2, 'DEBIT', 1000, 'USD', 10000, 9000, now())
	`, walletID, pendingBetID)

	mustFail(t, ctx, pool, `
		INSERT INTO wallet_ledger_entries (
			id, wallet_id, transaction_id, direction, amount_minor, currency,
			balance_before_minor, balance_after_minor, created_at
		) VALUES ('24242424-2424-7424-8424-242424242424', $1, $2, 'DEBIT', 1000, 'BRL', 10000, 9000, now())
	`, secondWallet, pendingBetID)

	mustFail(t, ctx, pool, `UPDATE wallet_ledger_entries SET amount_minor = 1 WHERE id = $1`, ledgerID)
	mustFail(t, ctx, pool, `DELETE FROM wallet_ledger_entries WHERE id = $1`, ledgerID)
}

func TestTerminalTransactionImmutability(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	insertWallet(t, ctx, pool, walletID, playerID, "BRL", 9000)
	insertProcessedBet(t, ctx, pool, betID, betExternalID, "idem-terminal", walletID, playerID, 1000)

	mustFail(t, ctx, pool, `UPDATE wager_transactions SET status = 'REJECTED', failure_code = 'X' WHERE id = $1`, betID)
	mustFail(t, ctx, pool, `DELETE FROM wager_transactions WHERE id = $1`, betID)

	insertPendingBet(t, ctx, pool, pendingBetID, "bet-pending-terminal", "idem-pending-terminal", walletID, playerID, 1000)

	mustExec(t, ctx, pool, `
		UPDATE wager_transactions
		SET status = 'PROCESSED', result_balance_minor = 9000, result_currency = 'BRL',
		    processed_at = now(), updated_at = now()
		WHERE id = $1
	`, pendingBetID)
}

func TestOutboxImmutability(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	mustExec(t, ctx, pool, `
		INSERT INTO outbox_events (
			event_id, aggregate_type, aggregate_id, event_type, event_version, payload,
			correlation_id, occurred_at, created_at
		) VALUES ($1, 'wallet', $2, 'WalletBalanceChanged', 1, '{"amount":"1.00"}',
			'corr-1', now(), now())
	`, outboxID, walletID)

	mustFail(t, ctx, pool, `UPDATE outbox_events SET payload = '{"amount":"2.00"}' WHERE event_id = $1`, outboxID)
	mustFail(t, ctx, pool, `UPDATE outbox_events SET event_type = 'Other' WHERE event_id = $1`, outboxID)

	mustExec(t, ctx, pool, `
		UPDATE outbox_events SET attempts = attempts + 1, next_attempt_at = now() + interval '1 minute'
		WHERE event_id = $1
	`, outboxID)
}

func TestLedgerRejectsMissingTransaction(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	insertWallet(t, ctx, pool, walletID, playerID, "BRL", 10000)

	mustFail(t, ctx, pool, `
		INSERT INTO wallet_ledger_entries (
			id, wallet_id, transaction_id, direction, amount_minor, currency,
			balance_before_minor, balance_after_minor, created_at
		) VALUES ('25252525-2525-7525-8525-252525252525', $1, $2, 'CREDIT', 100, 'BRL', 0, 100, now())
	`, walletID, missingTXID)
}

func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, _ := newTestDatabase(t, true)
	return pool
}

func newTestDatabase(t *testing.T, applyMigrations bool) (*pgxpool.Pool, string) {
	t.Helper()
	ctx := context.Background()

	baseURL := baseDatabaseURL()
	cfg, err := pgx.ParseConfig(baseURL)
	if err != nil {
		t.Fatalf("parse DATABASE_URL: %v", err)
	}

	adminConfig := cfg.Copy()
	adminConfig.Database = "postgres"

	admin, err := pgx.ConnectConfig(ctx, adminConfig)
	if err != nil {
		t.Fatalf("connect admin database: %v", err)
	}

	name := fmt.Sprintf("jungle_test_%d", time.Now().UnixNano())
	quotedName := pgx.Identifier{name}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+quotedName); err != nil {
		admin.Close(ctx)
		t.Fatalf("create test database: %v", err)
	}

	dsn, err := databaseURLWithName(baseURL, name)
	if err != nil {
		admin.Close(ctx)
		t.Fatalf("build test database URL: %v", err)
	}

	if applyMigrations {
		if err := migrate.Up(dsn); err != nil {
			admin.Close(ctx)
			t.Fatalf("apply migrations: %v", err)
		}
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		admin.Close(ctx)
		t.Fatalf("connect test database: %v", err)
	}

	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS "+quotedName+" WITH (FORCE)")
		_ = admin.Close(context.Background())
	})

	return pool, dsn
}

func baseDatabaseURL() string {
	if value := os.Getenv("DATABASE_URL"); value != "" {
		return value
	}
	return "postgres://jungle:jungle@localhost:5432/jungle?sslmode=disable"
}

func databaseURLWithName(base, name string) (string, error) {
	parsed, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	parsed.Path = "/" + name
	return parsed.String(), nil
}

func assertVersion(t *testing.T, dsn string, want uint) {
	t.Helper()
	version, dirty, err := migrate.Version(dsn)
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	if dirty {
		t.Fatalf("Version() dirty = true, want false")
	}
	if version != want {
		t.Fatalf("Version() = %d, want %d", version, want)
	}
}

func insertWallet(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id, player, currency string, balance int64) {
	t.Helper()
	mustExec(t, ctx, pool, `
		INSERT INTO wallets (id, player_id, currency, balance_minor, version, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 1, now(), now())
	`, id, player, currency, balance)
}

func insertOpening(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id, wallet, player string, amount int64, reference string) {
	t.Helper()
	mustExec(t, ctx, pool, `
		INSERT INTO wager_transactions (
			id, origin, kind, status, internal_reference, wallet_id, player_id, amount_minor,
			currency, result_balance_minor, result_currency, created_at, updated_at, processed_at
		) VALUES ($1, 'INTERNAL', 'OPENING', 'PROCESSED', $2, $3, $4, $5, 'BRL', $5, 'BRL', now(), now(), now())
	`, id, reference, wallet, player, amount)
}

func insertProcessedBet(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id, externalID, idempotencyKey, wallet, player string, amount int64) {
	t.Helper()
	mustExec(t, ctx, pool, `
		INSERT INTO wager_transactions (
			id, origin, kind, status, provider_id, external_transaction_id, idempotency_key,
			payload_hash, wallet_id, player_id, round_id, game_id, amount_minor, currency,
			result_balance_minor, result_currency, created_at, updated_at, processed_at
		) VALUES ($1, 'EXTERNAL', 'BET', 'PROCESSED', 'provider-a', $2, $3, 'hash', $4, $5,
			'round-1', 'game-1', $6, 'BRL', 9000, 'BRL', now(), now(), now())
	`, id, externalID, idempotencyKey, wallet, player, amount)
}

func insertPendingBet(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id, externalID, idempotencyKey, wallet, player string, amount int64) {
	t.Helper()
	mustExec(t, ctx, pool, `
		INSERT INTO wager_transactions (
			id, origin, kind, status, provider_id, external_transaction_id, idempotency_key,
			payload_hash, wallet_id, player_id, round_id, game_id, amount_minor, currency,
			created_at, updated_at
		) VALUES ($1, 'EXTERNAL', 'BET', 'PENDING', 'provider-a', $2, $3, 'hash', $4, $5,
			'round-1', 'game-1', $6, 'BRL', now(), now())
	`, id, externalID, idempotencyKey, wallet, player, amount)
}

func insertReversal(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id, externalID, idempotencyKey, kind, referenceID, referenceExternalID, wallet, player string, amount int64) {
	t.Helper()
	mustExec(t, ctx, pool, `
		INSERT INTO wager_transactions (
			id, origin, kind, status, provider_id, external_transaction_id, idempotency_key,
			payload_hash, wallet_id, player_id, round_id, game_id, amount_minor, currency,
			reference_external_transaction_id, reference_transaction_id, result_balance_minor,
			result_currency, created_at, updated_at, processed_at
		) VALUES ($1, 'EXTERNAL', $2, 'PROCESSED', 'provider-a', $3, $4, 'hash', $5, $6,
			'round-1', 'game-1', $7, 'BRL', $8, $9, 9000, 'BRL', now(), now(), now())
	`, id, kind, externalID, idempotencyKey, wallet, player, amount, referenceExternalID, referenceID)
}

func mustExec(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("exec failed: %v", err)
	}
}

func mustFail(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(ctx, sql, args...); err == nil {
		t.Fatal("expected database error, got nil")
	}
}
