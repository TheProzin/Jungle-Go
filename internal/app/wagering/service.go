package wagering

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TheProzin/Jungle-Go/internal/app/ports"
	"github.com/TheProzin/Jungle-Go/internal/config"
	"github.com/TheProzin/Jungle-Go/internal/domain/events"
	"github.com/TheProzin/Jungle-Go/internal/domain/ledger"
	"github.com/TheProzin/Jungle-Go/internal/domain/money"
	domainwagering "github.com/TheProzin/Jungle-Go/internal/domain/wagering"
	domainwallet "github.com/TheProzin/Jungle-Go/internal/domain/wallet"
	"github.com/TheProzin/Jungle-Go/internal/observability"
	"github.com/TheProzin/Jungle-Go/internal/platform/failpoint"
)

var (
	ErrInvalidCommand    = errors.New("invalid transaction command")
	ErrReferenceRequired = errors.New("reference is required")
	ErrDuplicateMessage  = errors.New("duplicate inbox message")
	ErrInboxHashMismatch = errors.New("inbox payload hash mismatch")
)

type InboxIdentity struct {
	ConsumerName string
	MessageID    string
}

type Service struct {
	uow   ports.UnitOfWork
	newID func() (uuid.UUID, error)
	now   func() time.Time

	referenceInitialBackoff time.Duration
	referenceMaxBackoff     time.Duration
	referenceMaxAttempts    int
	referenceTTL            time.Duration
}

type ProcessCommand struct {
	ProviderID                     string
	ExternalTransactionID          string
	IdempotencyKey                 string
	PlayerID                       uuid.UUID
	WalletID                       uuid.UUID
	RoundID                        string
	GameID                         string
	Kind                           domainwagering.Kind
	Amount                         money.Money
	ReferenceExternalTransactionID string
	CorrelationID                  string
}

type ProcessResult struct {
	Transaction      *domainwagering.Transaction
	Balance          *money.Money
	IdempotentReplay bool
}

func NewService(uow ports.UnitOfWork) *Service {
	return newService(uow, time.Second, 5*time.Minute, 10, time.Hour)
}

func NewServiceWithConfig(uow ports.UnitOfWork, cfg *config.Config) *Service {
	return newService(
		uow,
		cfg.ReferenceInitialBackoff,
		cfg.ReferenceMaxBackoff,
		cfg.ReferenceMaxAttempts,
		cfg.ReferenceTTL,
	)
}

func newService(
	uow ports.UnitOfWork,
	initialBackoff, maxBackoff time.Duration,
	maxAttempts int,
	ttl time.Duration,
) *Service {
	return &Service{
		uow:                     uow,
		newID:                   uuid.NewV7,
		now:                     func() time.Time { return time.Now().UTC() },
		referenceInitialBackoff: initialBackoff,
		referenceMaxBackoff:     maxBackoff,
		referenceMaxAttempts:    maxAttempts,
		referenceTTL:            ttl,
	}
}

func (s *Service) ProcessTransaction(ctx context.Context, cmd ProcessCommand) (*ProcessResult, error) {
	return s.processTransaction(ctx, cmd, nil)
}

func (s *Service) ProcessTransactionWithInbox(
	ctx context.Context,
	cmd ProcessCommand,
	identity InboxIdentity,
) (*ProcessResult, error) {
	return s.processTransaction(ctx, cmd, &identity)
}

func (s *Service) MarkInboxFailed(ctx context.Context, consumerName, messageID, reason string) error {
	now := s.now()
	return s.uow.Do(ctx, func(repos ports.Repositories) error {
		return repos.Inbox.MarkFailed(ctx, consumerName, messageID, reason, now)
	})
}

func (s *Service) processTransaction(
	ctx context.Context,
	cmd ProcessCommand,
	inbox *InboxIdentity,
) (*ProcessResult, error) {
	if err := validateCommand(cmd); err != nil {
		return nil, err
	}
	if cmd.CorrelationID == "" {
		cmd.CorrelationID = uuid.NewString()
	}

	hash, err := FingerprintFromCommand(cmd).Hash()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidCommand, err)
	}

	transactionID, err := s.newID()
	if err != nil {
		return nil, fmt.Errorf("generate transaction id: %w", err)
	}
	now := s.now()

	var (
		existing *domainwagering.Transaction
		created  *domainwagering.Transaction
	)

	err = s.uow.Do(ctx, func(repos ports.Repositories) error {
		if inbox != nil {
			inserted, existingHash, err := repos.Inbox.Insert(
				ctx,
				inbox.ConsumerName,
				inbox.MessageID,
				hash,
				now,
			)
			if err != nil {
				return err
			}
			if !inserted {
				if existingHash != hash {
					return ErrInboxHashMismatch
				}
				return ErrDuplicateMessage
			}
		}

		found, err := findExisting(ctx, repos, cmd, hash)
		if err != nil {
			return err
		}
		if found != nil {
			existing = found
			if inbox != nil {
				return repos.Inbox.Complete(ctx, inbox.ConsumerName, inbox.MessageID, now)
			}
			return nil
		}

		transaction, err := s.apply(ctx, repos, cmd, hash, transactionID, now)
		if err != nil {
			return err
		}
		created = transaction

		if inbox != nil {
			return repos.Inbox.Complete(ctx, inbox.ConsumerName, inbox.MessageID, now)
		}
		return nil
	})

	if errors.Is(err, ErrDuplicateMessage) || errors.Is(err, ErrInboxHashMismatch) {
		return nil, err
	}
	if errors.Is(err, domainwagering.ErrIdempotencyKeyConflict) ||
		errors.Is(err, domainwagering.ErrExternalTransactionConflict) {
		observability.Default.RecordIdempotencyConflict()
	}
	if errors.Is(err, domainwagering.ErrDuplicateIdentity) {
		observability.Default.RecordConcurrencyConflict()
		if inbox != nil {
			return s.replayWithInbox(ctx, cmd, *inbox, hash)
		}
		return s.replay(ctx, cmd, hash)
	}
	if errors.Is(err, domainwallet.ErrConcurrentUpdate) {
		observability.Default.RecordConcurrencyConflict()
	}
	if err != nil {
		return nil, err
	}
	if created != nil && created.Status() == domainwagering.StatusPendingReference {
		if hookErr := failpoint.Trigger("pending-after-commit"); hookErr != nil {
			return nil, hookErr
		}
	}
	if existing != nil {
		observability.Default.RecordIdempotentReplay()
		observability.Default.RecordTransaction(string(existing.Status()), string(existing.Kind()))
		return replayResult(existing), nil
	}
	observability.Default.RecordTransaction(string(created.Status()), string(created.Kind()))
	return resultFor(created, false), nil
}

func (s *Service) replayWithInbox(
	ctx context.Context,
	cmd ProcessCommand,
	inbox InboxIdentity,
	hash string,
) (*ProcessResult, error) {
	now := s.now()
	var result *ProcessResult

	err := s.uow.Do(ctx, func(repos ports.Repositories) error {
		inserted, existingHash, err := repos.Inbox.Insert(
			ctx,
			inbox.ConsumerName,
			inbox.MessageID,
			hash,
			now,
		)
		if err != nil {
			return err
		}
		if !inserted && existingHash != hash {
			return ErrInboxHashMismatch
		}

		found, err := findExisting(ctx, repos, cmd, hash)
		if err != nil {
			return err
		}
		if found == nil {
			return domainwagering.ErrDuplicateIdentity
		}

		result = replayResult(found)
		if inserted {
			return repos.Inbox.Complete(ctx, inbox.ConsumerName, inbox.MessageID, now)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	observability.Default.RecordIdempotentReplay()
	observability.Default.RecordTransaction(
		string(result.Transaction.Status()),
		string(result.Transaction.Kind()),
	)
	return result, nil
}

func (s *Service) GetTransaction(ctx context.Context, transactionID uuid.UUID) (*domainwagering.Transaction, error) {
	var found *domainwagering.Transaction
	err := s.uow.DoRead(ctx, func(repos ports.Repositories) error {
		transaction, err := repos.Transactions.FindByID(ctx, transactionID)
		if err != nil {
			return err
		}
		found = transaction
		return nil
	})
	if err != nil {
		return nil, err
	}
	return found, nil
}

func (s *Service) GetProviderTransaction(ctx context.Context, providerID, externalTransactionID string) (*domainwagering.Transaction, error) {
	var found *domainwagering.Transaction
	err := s.uow.DoRead(ctx, func(repos ports.Repositories) error {
		transaction, err := repos.Transactions.FindByProviderAndExternalID(ctx, providerID, externalTransactionID)
		if err != nil {
			return err
		}
		found = transaction
		return nil
	})
	if err != nil {
		return nil, err
	}
	return found, nil
}

func (s *Service) apply(
	ctx context.Context,
	repos ports.Repositories,
	cmd ProcessCommand,
	hash string,
	transactionID uuid.UUID,
	now time.Time,
) (*domainwagering.Transaction, error) {
	wallet, err := repos.Wallets.GetByIDForUpdate(ctx, cmd.WalletID)
	if err != nil {
		return nil, err
	}

	var (
		reference        *domainwagering.Transaction
		referenceID      *uuid.UUID
		referenceFailure string
	)

	switch cmd.Kind {
	case domainwagering.KindWin:
		referenceID, referenceFailure, err = resolveWinReference(ctx, repos, cmd)
		if err != nil {
			return nil, err
		}
	case domainwagering.KindRefund, domainwagering.KindRollback:
		reference, err = findReversalReference(ctx, repos, cmd)
		if err != nil {
			return nil, err
		}
		if reference != nil {
			id := reference.ID()
			referenceID = &id
		}
	}

	transaction, err := domainwagering.NewExternal(domainwagering.ExternalInput{
		ID:                             transactionID,
		ProviderID:                     cmd.ProviderID,
		ExternalTransactionID:          cmd.ExternalTransactionID,
		IdempotencyKey:                 cmd.IdempotencyKey,
		PayloadHash:                    hash,
		WalletID:                       cmd.WalletID,
		PlayerID:                       cmd.PlayerID,
		RoundID:                        cmd.RoundID,
		GameID:                         cmd.GameID,
		Kind:                           cmd.Kind,
		Amount:                         cmd.Amount,
		ReferenceExternalTransactionID: cmd.ReferenceExternalTransactionID,
		ReferenceTransactionID:         referenceID,
		Now:                            now,
	})
	if err != nil {
		return nil, err
	}

	if referenceFailure != "" {
		if _, err := s.rejectTransaction(ctx, repos, transaction, referenceFailure, cmd.CorrelationID, now, repos.Transactions.Create); err != nil {
			return nil, err
		}
		return transaction, nil
	}
	if wallet.PlayerID() != cmd.PlayerID {
		if _, err := s.rejectTransaction(ctx, repos, transaction, "WALLET_PLAYER_MISMATCH", cmd.CorrelationID, now, repos.Transactions.Create); err != nil {
			return nil, err
		}
		return transaction, nil
	}
	if wallet.Balance().Currency() != cmd.Amount.Currency() {
		if _, err := s.rejectTransaction(ctx, repos, transaction, "WALLET_CURRENCY_MISMATCH", cmd.CorrelationID, now, repos.Transactions.Create); err != nil {
			return nil, err
		}
		return transaction, nil
	}

	if cmd.Kind == domainwagering.KindRefund || cmd.Kind == domainwagering.KindRollback {
		return s.handleReversal(
			ctx,
			repos,
			transaction,
			reference,
			wallet,
			cmd.CorrelationID,
			now,
			repos.Transactions.Create,
		)
	}

	oldBalance := wallet.Balance()
	oldVersion := wallet.Version()

	switch cmd.Kind {
	case domainwagering.KindBet:
		if err := wallet.Debit(cmd.Amount, now); err != nil {
			if errors.Is(err, domainwallet.ErrInsufficientFunds) {
				if _, err := s.rejectTransaction(ctx, repos, transaction, "INSUFFICIENT_FUNDS", cmd.CorrelationID, now, repos.Transactions.Create); err != nil {
					return nil, err
				}
				return transaction, nil
			}
			return nil, err
		}
	case domainwagering.KindWin:
		if err := wallet.Credit(cmd.Amount, now); err != nil {
			return nil, err
		}
	case domainwagering.KindLoss:
	}

	if err := transaction.MarkProcessed(wallet.Balance(), now); err != nil {
		return nil, err
	}
	if err := repos.Transactions.Create(ctx, transaction); err != nil {
		return nil, err
	}

	if cmd.Kind != domainwagering.KindLoss {
		var entry *ledger.Entry
		entryID, err := s.newID()
		if err != nil {
			return nil, fmt.Errorf("generate ledger id: %w", err)
		}

		if cmd.Kind == domainwagering.KindBet {
			entry, err = ledger.NewDebit(entryID, wallet.ID(), transaction.ID(), cmd.Amount, oldBalance, wallet.Balance(), now)
		} else {
			entry, err = ledger.NewCredit(entryID, wallet.ID(), transaction.ID(), cmd.Amount, oldBalance, wallet.Balance(), now)
		}
		if err != nil {
			return nil, err
		}
		if err := repos.Ledger.Create(ctx, entry); err != nil {
			return nil, err
		}
		if err := repos.Wallets.UpdateBalance(ctx, wallet, oldVersion); err != nil {
			return nil, err
		}
	}

	if err := s.insertProcessedEvent(ctx, repos, transaction, cmd.CorrelationID, now); err != nil {
		return nil, err
	}
	if cmd.Kind != domainwagering.KindLoss {
		if err := s.insertBalanceChangedEvent(ctx, repos, transaction, oldBalance, wallet, cmd.CorrelationID, now); err != nil {
			return nil, err
		}
	}

	return transaction, nil
}

type persistFunc func(context.Context, *domainwagering.Transaction) error

func (s *Service) rejectTransaction(
	ctx context.Context,
	repos ports.Repositories,
	transaction *domainwagering.Transaction,
	failureCode, correlationID string,
	now time.Time,
	persist persistFunc,
) (*domainwagering.Transaction, error) {
	if err := transaction.MarkRejected(failureCode, now); err != nil {
		return nil, err
	}
	if err := persist(ctx, transaction); err != nil {
		return nil, err
	}
	if err := s.insertRejectedEvent(ctx, repos, transaction, failureCode, correlationID, now); err != nil {
		return nil, err
	}
	return transaction, nil
}

func (s *Service) insertRejectedEvent(
	ctx context.Context,
	repos ports.Repositories,
	transaction *domainwagering.Transaction,
	failureCode, correlationID string,
	now time.Time,
) error {
	eventID, err := s.newID()
	if err != nil {
		return fmt.Errorf("generate rejected event id: %w", err)
	}
	event, err := events.NewWagerTransactionRejected(
		eventID,
		transaction.ID().String(),
		correlationID,
		nil,
		now,
		events.WagerTransactionRejectedData{
			TransactionID:         transaction.ID().String(),
			ProviderID:            stringValue(transaction.ProviderID()),
			ExternalTransactionID: stringValue(transaction.ExternalTransactionID()),
			Kind:                  string(transaction.Kind()),
			FailureCode:           failureCode,
		},
	)
	if err != nil {
		return err
	}
	return repos.Outbox.Insert(ctx, event)
}

func (s *Service) insertProcessedEvent(
	ctx context.Context,
	repos ports.Repositories,
	transaction *domainwagering.Transaction,
	correlationID string,
	now time.Time,
) error {
	eventID, err := s.newID()
	if err != nil {
		return fmt.Errorf("generate processed event id: %w", err)
	}
	event, err := events.NewWagerTransactionProcessed(
		eventID,
		transaction.ID().String(),
		correlationID,
		nil,
		now,
		events.WagerTransactionProcessedData{
			TransactionID:         transaction.ID().String(),
			ProviderID:            stringValue(transaction.ProviderID()),
			ExternalTransactionID: stringValue(transaction.ExternalTransactionID()),
			Kind:                  string(transaction.Kind()),
			WalletID:              transaction.WalletID().String(),
			PlayerID:              transaction.PlayerID().String(),
			RoundID:               stringValue(transaction.RoundID()),
			GameID:                stringValue(transaction.GameID()),
			Money:                 transaction.Amount(),
			Status:                string(transaction.Status()),
		},
	)
	if err != nil {
		return err
	}
	return repos.Outbox.Insert(ctx, event)
}

func (s *Service) insertBalanceChangedEvent(
	ctx context.Context,
	repos ports.Repositories,
	transaction *domainwagering.Transaction,
	oldBalance money.Money,
	wallet *domainwallet.Wallet,
	correlationID string,
	now time.Time,
) error {
	eventID, err := s.newID()
	if err != nil {
		return fmt.Errorf("generate balance event id: %w", err)
	}
	causationID := transaction.ID().String()

	direction := ledger.DirectionCredit
	if oldBalance.Minor() > wallet.Balance().Minor() {
		direction = ledger.DirectionDebit
	}

	event, err := events.NewWalletBalanceChanged(
		eventID,
		wallet.ID().String(),
		correlationID,
		&causationID,
		now,
		events.WalletBalanceChangedData{
			WalletID:      wallet.ID().String(),
			TransactionID: transaction.ID().String(),
			Direction:     direction,
			Money:         transaction.Amount(),
			BalanceBefore: oldBalance,
			BalanceAfter:  wallet.Balance(),
			WalletVersion: wallet.Version(),
		},
	)
	if err != nil {
		return err
	}
	return repos.Outbox.Insert(ctx, event)
}

func findReversalReference(ctx context.Context, repos ports.Repositories, cmd ProcessCommand) (*domainwagering.Transaction, error) {
	reference, err := repos.Transactions.FindByProviderAndExternalID(ctx, cmd.ProviderID, cmd.ReferenceExternalTransactionID)
	if errors.Is(err, domainwagering.ErrTransactionNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return repos.Transactions.FindByIDForUpdate(ctx, reference.ID())
}

func (s *Service) handleReversal(
	ctx context.Context,
	repos ports.Repositories,
	transaction, reference *domainwagering.Transaction,
	wallet *domainwallet.Wallet,
	correlationID string,
	now time.Time,
	persist persistFunc,
) (*domainwagering.Transaction, error) {
	if reference == nil {
		return s.schedulePending(ctx, repos, transaction, correlationID, now, persist)
	}

	switch reference.Status() {
	case domainwagering.StatusPending, domainwagering.StatusPendingReference:
		if err := transaction.SetReferenceTransactionID(reference.ID()); err != nil {
			return nil, err
		}
		return s.schedulePending(ctx, repos, transaction, correlationID, now, persist)
	case domainwagering.StatusRejected, domainwagering.StatusFailed:
		if err := transaction.SetReferenceTransactionID(reference.ID()); err != nil {
			return nil, err
		}
		return s.rejectTransaction(ctx, repos, transaction, "REFERENCE_NOT_PROCESSED", correlationID, now, persist)
	case domainwagering.StatusProcessed:
		return s.completeReversal(ctx, repos, transaction, reference, wallet, correlationID, now, persist)
	default:
		return nil, domainwagering.ErrInvalidTransaction
	}
}

func (s *Service) schedulePending(
	ctx context.Context,
	repos ports.Repositories,
	transaction *domainwagering.Transaction,
	correlationID string,
	now time.Time,
	persist persistFunc,
) (*domainwagering.Transaction, error) {
	nextAttemptAt := now.Add(s.referenceInitialBackoff)
	if err := transaction.MarkPendingReference(nextAttemptAt, now); err != nil {
		return nil, err
	}
	if err := persist(ctx, transaction); err != nil {
		return nil, err
	}
	if err := s.insertPendingReferenceEvent(ctx, repos, transaction, correlationID, now); err != nil {
		return nil, err
	}
	return transaction, nil
}

func (s *Service) insertPendingReferenceEvent(
	ctx context.Context,
	repos ports.Repositories,
	transaction *domainwagering.Transaction,
	correlationID string,
	now time.Time,
) error {
	nextAttemptAt := transaction.NextReferenceAt()
	if nextAttemptAt == nil {
		return fmt.Errorf("%w: next reference attempt is missing", ErrInvalidCommand)
	}

	eventID, err := s.newID()
	if err != nil {
		return fmt.Errorf("generate pending reference event id: %w", err)
	}
	event, err := events.NewWagerTransactionPendingReference(
		eventID,
		transaction.ID().String(),
		correlationID,
		nil,
		now,
		events.WagerTransactionPendingReferenceData{
			TransactionID:                  transaction.ID().String(),
			ReferenceExternalTransactionID: stringValue(transaction.ReferenceExternalTransactionID()),
			Attempts:                       transaction.ReferenceAttempts(),
			NextAttemptAt:                  *nextAttemptAt,
		},
	)
	if err != nil {
		return err
	}
	return repos.Outbox.Insert(ctx, event)
}

func (s *Service) completeReversal(
	ctx context.Context,
	repos ports.Repositories,
	transaction, reference *domainwagering.Transaction,
	wallet *domainwallet.Wallet,
	correlationID string,
	now time.Time,
	persist persistFunc,
) (*domainwagering.Transaction, error) {
	if failureCode := validateReversalAgainstReference(transaction, reference); failureCode != "" {
		if err := transaction.SetReferenceTransactionID(reference.ID()); err != nil {
			return nil, err
		}
		return s.rejectTransaction(ctx, repos, transaction, failureCode, correlationID, now, persist)
	}

	exists, err := repos.Transactions.ExistsSuccessfulReversal(ctx, reference.ID())
	if err != nil {
		return nil, err
	}
	if exists {
		if err := transaction.SetReferenceTransactionID(reference.ID()); err != nil {
			return nil, err
		}
		return s.rejectTransaction(ctx, repos, transaction, "DUPLICATE_REVERSAL", correlationID, now, persist)
	}

	direction, err := reversalDirection(transaction.Kind(), reference.Kind())
	if err != nil {
		return nil, err
	}
	if err := transaction.SetReferenceTransactionID(reference.ID()); err != nil {
		return nil, err
	}

	oldBalance := wallet.Balance()
	oldVersion := wallet.Version()

	switch direction {
	case ledger.DirectionCredit:
		if err := wallet.Credit(transaction.Amount(), now); err != nil {
			return nil, err
		}
	case ledger.DirectionDebit:
		if err := wallet.Debit(transaction.Amount(), now); err != nil {
			if errors.Is(err, domainwallet.ErrInsufficientFunds) {
				return s.rejectTransaction(
					ctx,
					repos,
					transaction,
					"INSUFFICIENT_FUNDS_FOR_REVERSAL",
					correlationID,
					now,
					persist,
				)
			}
			return nil, err
		}
	}

	if err := transaction.MarkProcessed(wallet.Balance(), now); err != nil {
		return nil, err
	}
	if err := persist(ctx, transaction); err != nil {
		return nil, err
	}

	entryID, err := s.newID()
	if err != nil {
		return nil, fmt.Errorf("generate reversal ledger id: %w", err)
	}

	var entry *ledger.Entry
	if direction == ledger.DirectionCredit {
		entry, err = ledger.NewCredit(
			entryID, wallet.ID(), transaction.ID(), transaction.Amount(),
			oldBalance, wallet.Balance(), now,
		)
	} else {
		entry, err = ledger.NewDebit(
			entryID, wallet.ID(), transaction.ID(), transaction.Amount(),
			oldBalance, wallet.Balance(), now,
		)
	}
	if err != nil {
		return nil, err
	}
	if err := repos.Ledger.Create(ctx, entry); err != nil {
		return nil, err
	}
	if err := repos.Wallets.UpdateBalance(ctx, wallet, oldVersion); err != nil {
		return nil, err
	}
	if err := s.insertProcessedEvent(ctx, repos, transaction, correlationID, now); err != nil {
		return nil, err
	}
	if err := s.insertBalanceChangedEvent(ctx, repos, transaction, oldBalance, wallet, correlationID, now); err != nil {
		return nil, err
	}

	return transaction, nil
}

func validateReversalAgainstReference(transaction, reference *domainwagering.Transaction) string {
	switch transaction.Kind() {
	case domainwagering.KindRefund:
		if reference.Kind() != domainwagering.KindBet {
			return "REFERENCE_MISMATCH"
		}
	case domainwagering.KindRollback:
		if reference.Kind() != domainwagering.KindBet &&
			reference.Kind() != domainwagering.KindWin &&
			reference.Kind() != domainwagering.KindRefund {
			return "REFERENCE_MISMATCH"
		}
	default:
		return "REFERENCE_MISMATCH"
	}

	if reference.WalletID() != transaction.WalletID() ||
		reference.PlayerID() != transaction.PlayerID() ||
		reference.Amount().Currency() != transaction.Amount().Currency() ||
		reference.RoundID() == nil ||
		transaction.RoundID() == nil ||
		*reference.RoundID() != *transaction.RoundID() {
		return "REFERENCE_MISMATCH"
	}
	if !reference.Amount().Equal(transaction.Amount()) {
		return "REFERENCE_AMOUNT_MISMATCH"
	}
	return ""
}

func reversalDirection(kind, referenceKind domainwagering.Kind) (ledger.Direction, error) {
	switch kind {
	case domainwagering.KindRefund:
		return ledger.DirectionCredit, nil
	case domainwagering.KindRollback:
		switch referenceKind {
		case domainwagering.KindBet:
			return ledger.DirectionCredit, nil
		case domainwagering.KindWin, domainwagering.KindRefund:
			return ledger.DirectionDebit, nil
		}
	}
	return "", domainwagering.ErrInvalidTransaction
}

func (s *Service) ProcessNextPendingReference(ctx context.Context) (bool, error) {
	processed := false
	err := s.uow.Do(ctx, func(repos ports.Repositories) error {
		pending, err := repos.Transactions.FindNextPendingReferenceForUpdate(ctx)
		if err != nil {
			return err
		}
		if pending == nil {
			return nil
		}
		processed = true
		return s.processPendingReference(ctx, repos, pending)
	})
	return processed, err
}

func (s *Service) RefreshReferenceMetrics(ctx context.Context) {
	var count int
	err := s.uow.DoRead(ctx, func(repos ports.Repositories) error {
		var err error
		count, err = repos.Transactions.CountPendingReferences(ctx)
		return err
	})
	if err != nil {
		return
	}
	observability.Default.SetReferencePending(count)
}

func (s *Service) processPendingReference(
	ctx context.Context,
	repos ports.Repositories,
	pending *domainwagering.Transaction,
) error {
	observability.Default.RecordReferenceAttempt()
	defer func() {
		if pending.IsTerminal() {
			observability.Default.RecordTransaction(string(pending.Status()), string(pending.Kind()))
		}
	}()

	now := s.now()
	correlationID := pending.ID().String()

	wallet, err := repos.Wallets.GetByIDForUpdate(ctx, pending.WalletID())
	if err != nil {
		return err
	}

	referenceExternalID := stringValue(pending.ReferenceExternalTransactionID())
	if referenceExternalID == "" {
		return s.expireOrReschedule(ctx, repos, pending, "REFERENCE_NOT_FOUND", now)
	}

	reference, err := repos.Transactions.FindByProviderAndExternalID(
		ctx,
		stringValue(pending.ProviderID()),
		referenceExternalID,
	)
	if errors.Is(err, domainwagering.ErrTransactionNotFound) {
		return s.expireOrReschedule(ctx, repos, pending, "REFERENCE_NOT_FOUND", now)
	}
	if err != nil {
		return err
	}

	locked, err := repos.Transactions.FindByIDForUpdate(ctx, reference.ID())
	if err != nil {
		return err
	}
	if err := pending.SetReferenceTransactionID(locked.ID()); err != nil {
		return err
	}

	switch locked.Status() {
	case domainwagering.StatusPending, domainwagering.StatusPendingReference:
		return s.expireOrReschedule(ctx, repos, pending, "REFERENCE_NOT_PROCESSED", now)
	case domainwagering.StatusRejected, domainwagering.StatusFailed:
		_, err := s.rejectTransaction(
			ctx, repos, pending, "REFERENCE_NOT_PROCESSED", correlationID, now,
			repos.Transactions.Update,
		)
		return err
	case domainwagering.StatusProcessed:
		_, err := s.completeReversal(
			ctx, repos, pending, locked, wallet, correlationID, now,
			repos.Transactions.Update,
		)
		return err
	default:
		return domainwagering.ErrInvalidTransaction
	}
}

func (s *Service) expireOrReschedule(
	ctx context.Context,
	repos ports.Repositories,
	pending *domainwagering.Transaction,
	failureCode string,
	now time.Time,
) error {
	if s.referenceExpired(pending, now) {
		_, err := s.rejectTransaction(
			ctx, repos, pending, failureCode, pending.ID().String(), now,
			repos.Transactions.Update,
		)
		return err
	}

	nextAttemptAt := now.Add(s.referenceBackoff(pending.ReferenceAttempts() + 1))
	if err := pending.ReschedulePendingReference(nextAttemptAt, now); err != nil {
		return err
	}
	return repos.Transactions.Update(ctx, pending)
}

func (s *Service) referenceExpired(pending *domainwagering.Transaction, now time.Time) bool {
	return pending.ReferenceAttempts() >= s.referenceMaxAttempts ||
		now.Sub(pending.CreatedAt()) >= s.referenceTTL
}

func (s *Service) referenceBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}

	backoff := s.referenceInitialBackoff
	for i := 1; i < attempt; i++ {
		if backoff >= s.referenceMaxBackoff/2 {
			return s.referenceMaxBackoff
		}
		backoff *= 2
	}
	if backoff > s.referenceMaxBackoff {
		return s.referenceMaxBackoff
	}
	return backoff
}

func (s *Service) replay(ctx context.Context, cmd ProcessCommand, hash string) (*ProcessResult, error) {
	var existing *domainwagering.Transaction
	err := s.uow.DoRead(ctx, func(repos ports.Repositories) error {
		found, err := findExisting(ctx, repos, cmd, hash)
		if err != nil {
			return err
		}
		if found == nil {
			return domainwagering.ErrDuplicateIdentity
		}
		existing = found
		return nil
	})
	if err != nil {
		return nil, err
	}
	observability.Default.RecordIdempotentReplay()
	observability.Default.RecordTransaction(
		string(existing.Status()),
		string(existing.Kind()),
	)
	return replayResult(existing), nil
}

func findExisting(ctx context.Context, repos ports.Repositories, cmd ProcessCommand, hash string) (*domainwagering.Transaction, error) {
	transactions, err := repos.Transactions.FindByIdentity(
		ctx,
		cmd.ProviderID,
		cmd.IdempotencyKey,
		cmd.ExternalTransactionID,
	)
	if err != nil {
		return nil, err
	}

	var keyMatch, externalMatch *domainwagering.Transaction
	for _, transaction := range transactions {
		transactionKey := stringValue(transaction.IdempotencyKey())
		transactionExternalID := stringValue(transaction.ExternalTransactionID())

		if transactionKey == cmd.IdempotencyKey && transactionExternalID == cmd.ExternalTransactionID {
			if transaction.PayloadHash() == nil || *transaction.PayloadHash() != hash {
				return nil, domainwagering.ErrIdempotencyKeyConflict
			}
			return transaction, nil
		}
		if transactionExternalID == cmd.ExternalTransactionID {
			externalMatch = transaction
		}
		if transactionKey == cmd.IdempotencyKey {
			keyMatch = transaction
		}
	}

	if externalMatch != nil {
		return nil, domainwagering.ErrExternalTransactionConflict
	}
	if keyMatch != nil {
		return nil, domainwagering.ErrIdempotencyKeyConflict
	}
	return nil, nil
}

func resolveWinReference(ctx context.Context, repos ports.Repositories, cmd ProcessCommand) (*uuid.UUID, string, error) {
	if cmd.Kind != domainwagering.KindWin || cmd.ReferenceExternalTransactionID == "" {
		return nil, "", nil
	}

	reference, err := repos.Transactions.FindByProviderAndExternalID(ctx, cmd.ProviderID, cmd.ReferenceExternalTransactionID)
	if errors.Is(err, domainwagering.ErrTransactionNotFound) {
		return nil, "REFERENCE_NOT_FOUND", nil
	}
	if err != nil {
		return nil, "", err
	}

	if reference.Kind() != domainwagering.KindBet ||
		reference.Status() != domainwagering.StatusProcessed ||
		reference.WalletID() != cmd.WalletID ||
		reference.PlayerID() != cmd.PlayerID ||
		reference.Amount().Currency() != cmd.Amount.Currency() ||
		reference.RoundID() == nil || *reference.RoundID() != cmd.RoundID {
		id := reference.ID()
		return &id, "REFERENCE_MISMATCH", nil
	}

	id := reference.ID()
	return &id, "", nil
}

func validateCommand(cmd ProcessCommand) error {
	if cmd.ProviderID == "" || cmd.ExternalTransactionID == "" ||
		cmd.IdempotencyKey == "" || len(cmd.IdempotencyKey) > 255 ||
		cmd.RoundID == "" || cmd.GameID == "" {
		return fmt.Errorf("%w: required identity fields are missing", ErrInvalidCommand)
	}
	if cmd.PlayerID == uuid.Nil || cmd.WalletID == uuid.Nil {
		return fmt.Errorf("%w: player and wallet are required", ErrInvalidCommand)
	}
	if !cmd.Amount.IsValid() {
		return fmt.Errorf("%w: invalid amount", ErrInvalidCommand)
	}

	switch cmd.Kind {
	case domainwagering.KindBet, domainwagering.KindWin:
		if !cmd.Amount.IsPositive() {
			return fmt.Errorf("%w: amount must be positive", ErrInvalidCommand)
		}
	case domainwagering.KindLoss:
		if !cmd.Amount.IsZero() {
			return fmt.Errorf("%w: loss amount must be zero", ErrInvalidCommand)
		}
	case domainwagering.KindRefund, domainwagering.KindRollback:
		if !cmd.Amount.IsPositive() {
			return fmt.Errorf("%w: amount must be positive", ErrInvalidCommand)
		}
		if cmd.ReferenceExternalTransactionID == "" {
			return ErrReferenceRequired
		}
	case domainwagering.KindOpening:
		return fmt.Errorf("%w: opening is not allowed", ErrInvalidCommand)
	default:
		return fmt.Errorf("%w: unknown kind %q", ErrInvalidCommand, cmd.Kind)
	}

	if (cmd.Kind == domainwagering.KindBet || cmd.Kind == domainwagering.KindLoss) &&
		cmd.ReferenceExternalTransactionID != "" {
		return fmt.Errorf("%w: reference is not allowed for %s", ErrInvalidCommand, cmd.Kind)
	}
	return nil
}

func replayResult(transaction *domainwagering.Transaction) *ProcessResult {
	result := &ProcessResult{Transaction: transaction, IdempotentReplay: true}
	if transaction.Status() == domainwagering.StatusProcessed {
		result.Balance = transaction.ResultBalance()
	}
	return result
}

func resultFor(transaction *domainwagering.Transaction, replay bool) *ProcessResult {
	result := &ProcessResult{Transaction: transaction, IdempotentReplay: replay}
	if transaction.Status() == domainwagering.StatusProcessed {
		result.Balance = transaction.ResultBalance()
	}
	return result
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func FingerprintFromCommand(cmd ProcessCommand) domainwagering.Fingerprint {
	return domainwagering.Fingerprint{
		ProviderID:                     cmd.ProviderID,
		ExternalTransactionID:          cmd.ExternalTransactionID,
		PlayerID:                       cmd.PlayerID.String(),
		WalletID:                       cmd.WalletID.String(),
		RoundID:                        cmd.RoundID,
		GameID:                         cmd.GameID,
		Kind:                           cmd.Kind,
		Amount:                         cmd.Amount,
		ReferenceExternalTransactionID: cmd.ReferenceExternalTransactionID,
	}
}
