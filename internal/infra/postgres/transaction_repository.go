package postgres

import (
	"context"
	"fmt"

	"github.com/TheProzin/Jungle-Go/internal/domain/wagering"
)

type TransactionRepository struct {
	query querier
}

func (r *TransactionRepository) CreateOpening(ctx context.Context, transaction *wagering.Transaction) error {
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
		return fmt.Errorf("insert opening transaction: %w", err)
	}
	return nil
}
