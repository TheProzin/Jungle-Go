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
	"github.com/TheProzin/Jungle-Go/internal/domain/wagering"
)

const transactionColumns = `
	id, origin, kind, status, provider_id, external_transaction_id, idempotency_key,
	payload_hash, internal_reference, wallet_id, player_id, round_id, game_id,
	amount_minor, currency, reference_external_transaction_id, reference_transaction_id,
	failure_code, result_balance_minor, result_currency, reference_attempts,
	next_reference_at, created_at, updated_at, processed_at
`

type TransactionRepository struct {
	query querier
}

func (r *TransactionRepository) Create(ctx context.Context, transaction *wagering.Transaction) error {
	var (
		resultBalanceMinor *int64
		resultCurrency     *string
		referenceID        *string
	)

	if balance := transaction.ResultBalance(); balance != nil {
		minor := balance.Minor()
		currency := balance.Currency()
		resultBalanceMinor = &minor
		resultCurrency = &currency
	}
	if reference := transaction.ReferenceTransactionID(); reference != nil {
		value := reference.String()
		referenceID = &value
	}

	_, err := r.query.Exec(ctx, `
		INSERT INTO wager_transactions (
			id, origin, kind, status, provider_id, external_transaction_id, idempotency_key,
			payload_hash, internal_reference, wallet_id, player_id, round_id, game_id,
			amount_minor, currency, reference_external_transaction_id, reference_transaction_id,
			failure_code, result_balance_minor, result_currency, reference_attempts,
			next_reference_at, created_at, updated_at, processed_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17,
			$18, $19, $20, $21, $22, $23, $24, $25
		)
	`,
		transaction.ID().String(),
		string(transaction.Origin()),
		string(transaction.Kind()),
		string(transaction.Status()),
		transaction.ProviderID(),
		transaction.ExternalTransactionID(),
		transaction.IdempotencyKey(),
		transaction.PayloadHash(),
		transaction.InternalReference(),
		transaction.WalletID().String(),
		transaction.PlayerID().String(),
		transaction.RoundID(),
		transaction.GameID(),
		transaction.Amount().Minor(),
		transaction.Amount().Currency(),
		transaction.ReferenceExternalTransactionID(),
		referenceID,
		transaction.FailureCode(),
		resultBalanceMinor,
		resultCurrency,
		transaction.ReferenceAttempts(),
		transaction.NextReferenceAt(),
		transaction.CreatedAt(),
		transaction.UpdatedAt(),
		transaction.ProcessedAt(),
	)
	if err != nil {
		return mapTransactionWriteError(err)
	}
	return nil
}

func (r *TransactionRepository) Update(ctx context.Context, transaction *wagering.Transaction) error {
	var (
		referenceID        *string
		resultBalanceMinor *int64
		resultCurrency     *string
	)

	if reference := transaction.ReferenceTransactionID(); reference != nil {
		value := reference.String()
		referenceID = &value
	}
	if balance := transaction.ResultBalance(); balance != nil {
		minor := balance.Minor()
		currency := balance.Currency()
		resultBalanceMinor = &minor
		resultCurrency = &currency
	}

	tag, err := r.query.Exec(ctx, `
		UPDATE wager_transactions
		SET status = $1,
		    reference_transaction_id = $2,
		    failure_code = $3,
		    result_balance_minor = $4,
		    result_currency = $5,
		    reference_attempts = $6,
		    next_reference_at = $7,
		    updated_at = $8,
		    processed_at = $9
		WHERE id = $10
	`,
		string(transaction.Status()),
		referenceID,
		transaction.FailureCode(),
		resultBalanceMinor,
		resultCurrency,
		transaction.ReferenceAttempts(),
		transaction.NextReferenceAt(),
		transaction.UpdatedAt(),
		transaction.ProcessedAt(),
		transaction.ID().String(),
	)
	if err != nil {
		return mapTransactionWriteError(err)
	}
	if tag.RowsAffected() == 0 {
		return wagering.ErrTransactionNotFound
	}
	return nil
}

func (r *TransactionRepository) FindByID(ctx context.Context, id uuid.UUID) (*wagering.Transaction, error) {
	return scanTransaction(r.query.QueryRow(ctx, `
		SELECT `+transactionColumns+`
		FROM wager_transactions
		WHERE id = $1
	`, id.String()))
}

func (r *TransactionRepository) FindByIDForUpdate(ctx context.Context, id uuid.UUID) (*wagering.Transaction, error) {
	return scanTransaction(r.query.QueryRow(ctx, `
		SELECT `+transactionColumns+`
		FROM wager_transactions
		WHERE id = $1
		FOR UPDATE
	`, id.String()))
}

func (r *TransactionRepository) FindNextPendingReferenceForUpdate(ctx context.Context) (*wagering.Transaction, error) {
	transaction, err := scanTransaction(r.query.QueryRow(ctx, `
		SELECT `+transactionColumns+`
		FROM wager_transactions
		WHERE status = 'PENDING_REFERENCE'
		  AND next_reference_at <= now()
		ORDER BY next_reference_at
		FOR UPDATE SKIP LOCKED
		LIMIT 1
	`))
	if errors.Is(err, wagering.ErrTransactionNotFound) {
		return nil, nil
	}
	return transaction, err
}

func (r *TransactionRepository) ExistsSuccessfulReversal(ctx context.Context, referenceTransactionID uuid.UUID) (bool, error) {
	var exists bool
	err := r.query.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM wager_transactions
			WHERE reference_transaction_id = $1
			  AND kind IN ('REFUND', 'ROLLBACK')
			  AND status = 'PROCESSED'
		)
	`, referenceTransactionID.String()).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check successful reversal: %w", err)
	}
	return exists, nil
}

func (r *TransactionRepository) FindByProviderAndIdempotencyKey(
	ctx context.Context,
	providerID, idempotencyKey string,
) (*wagering.Transaction, error) {
	return scanTransaction(r.query.QueryRow(ctx, `
		SELECT `+transactionColumns+`
		FROM wager_transactions
		WHERE provider_id = $1 AND idempotency_key = $2 AND origin = 'EXTERNAL'
	`, providerID, idempotencyKey))
}

func (r *TransactionRepository) FindByProviderAndExternalID(
	ctx context.Context,
	providerID, externalTransactionID string,
) (*wagering.Transaction, error) {
	return scanTransaction(r.query.QueryRow(ctx, `
		SELECT `+transactionColumns+`
		FROM wager_transactions
		WHERE provider_id = $1 AND external_transaction_id = $2 AND origin = 'EXTERNAL'
	`, providerID, externalTransactionID))
}

func (r *TransactionRepository) FindByIdentity(
	ctx context.Context,
	providerID, idempotencyKey, externalTransactionID string,
) ([]*wagering.Transaction, error) {
	rows, err := r.query.Query(ctx, `
		SELECT `+transactionColumns+`
		FROM wager_transactions
		WHERE provider_id = $1
		  AND origin = 'EXTERNAL'
		  AND (idempotency_key = $2 OR external_transaction_id = $3)
		LIMIT 2
	`, providerID, idempotencyKey, externalTransactionID)
	if err != nil {
		return nil, fmt.Errorf("query transaction identity: %w", err)
	}
	defer rows.Close()

	transactions := make([]*wagering.Transaction, 0, 2)
	for rows.Next() {
		transaction, err := scanTransaction(rows)
		if err != nil {
			return nil, err
		}
		transactions = append(transactions, transaction)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate transaction identity: %w", err)
	}
	return transactions, nil
}

func scanTransaction(row pgx.Row) (*wagering.Transaction, error) {
	var (
		idText                         string
		origin, kind, status           string
		providerID                     *string
		externalTransactionID          *string
		idempotencyKey                 *string
		payloadHash                    *string
		internalReference              *string
		walletIDText                   string
		playerIDText                   string
		roundID                        *string
		gameID                         *string
		amountMinor                    int64
		currency                       string
		referenceExternalTransactionID *string
		referenceTransactionIDText     *string
		failureCode                    *string
		resultBalanceMinor             *int64
		resultCurrency                 *string
		referenceAttempts              int
		nextReferenceAt                *time.Time
		createdAt                      time.Time
		updatedAt                      time.Time
		processedAt                    *time.Time
	)

	err := row.Scan(
		&idText,
		&origin,
		&kind,
		&status,
		&providerID,
		&externalTransactionID,
		&idempotencyKey,
		&payloadHash,
		&internalReference,
		&walletIDText,
		&playerIDText,
		&roundID,
		&gameID,
		&amountMinor,
		&currency,
		&referenceExternalTransactionID,
		&referenceTransactionIDText,
		&failureCode,
		&resultBalanceMinor,
		&resultCurrency,
		&referenceAttempts,
		&nextReferenceAt,
		&createdAt,
		&updatedAt,
		&processedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, wagering.ErrTransactionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query transaction: %w", err)
	}

	id, err := uuid.Parse(idText)
	if err != nil {
		return nil, fmt.Errorf("parse transaction id: %w", err)
	}
	walletID, err := uuid.Parse(walletIDText)
	if err != nil {
		return nil, fmt.Errorf("parse transaction wallet id: %w", err)
	}
	playerID, err := uuid.Parse(playerIDText)
	if err != nil {
		return nil, fmt.Errorf("parse transaction player id: %w", err)
	}

	amount, err := money.New(amountMinor, strings.TrimSpace(currency))
	if err != nil {
		return nil, fmt.Errorf("parse transaction amount: %w", err)
	}

	var resultBalance *money.Money
	if resultBalanceMinor != nil && resultCurrency != nil {
		parsed, err := money.New(*resultBalanceMinor, strings.TrimSpace(*resultCurrency))
		if err != nil {
			return nil, fmt.Errorf("parse transaction result balance: %w", err)
		}
		resultBalance = &parsed
	}

	var referenceTransactionID *uuid.UUID
	if referenceTransactionIDText != nil {
		parsed, err := uuid.Parse(*referenceTransactionIDText)
		if err != nil {
			return nil, fmt.Errorf("parse reference transaction id: %w", err)
		}
		referenceTransactionID = &parsed
	}

	return wagering.Rehydrate(wagering.RehydrateInput{
		ID:                             id,
		Origin:                         wagering.Origin(origin),
		Kind:                           wagering.Kind(kind),
		Status:                         wagering.Status(status),
		ProviderID:                     providerID,
		ExternalTransactionID:          externalTransactionID,
		IdempotencyKey:                 idempotencyKey,
		PayloadHash:                    payloadHash,
		InternalReference:              internalReference,
		WalletID:                       walletID,
		PlayerID:                       playerID,
		RoundID:                        roundID,
		GameID:                         gameID,
		Amount:                         amount,
		ReferenceExternalTransactionID: referenceExternalTransactionID,
		ReferenceTransactionID:         referenceTransactionID,
		FailureCode:                    failureCode,
		ResultBalance:                  resultBalance,
		ReferenceAttempts:              referenceAttempts,
		NextReferenceAt:                nextReferenceAt,
		CreatedAt:                      createdAt,
		UpdatedAt:                      updatedAt,
		ProcessedAt:                    processedAt,
	})
}

func mapTransactionWriteError(err error) error {
	if err == nil {
		return nil
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		switch pgErr.ConstraintName {
		case "uq_tx_provider_external", "uq_tx_provider_idempotency":
			return wagering.ErrDuplicateIdentity
		case "uq_tx_single_reversal":
			return wagering.ErrDuplicateReversal
		}
	}
	return fmt.Errorf("write transaction: %w", err)
}
