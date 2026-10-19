package wagering

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/TheProzin/Jungle-Go/internal/domain/money"
)

func validExternalInput(t *testing.T) ExternalInput {
	t.Helper()
	amount, _ := money.Parse("25.00", "BRL")
	return ExternalInput{
		ID:                    testTransactionID,
		ProviderID:            "provider-a",
		ExternalTransactionID: "external-1",
		IdempotencyKey:        "provider-a:external-1",
		PayloadHash:           "hash-1",
		WalletID:              testWalletID,
		PlayerID:              testPlayerID,
		RoundID:               "round-1",
		GameID:                "game-1",
		Kind:                  KindBet,
		Amount:                amount,
		Now:                   testNow,
	}
}

func TestNewExternal(t *testing.T) {
	transaction, err := NewExternal(validExternalInput(t))
	if err != nil {
		t.Fatalf("NewExternal() error = %v", err)
	}
	if transaction.Status() != StatusPending {
		t.Fatalf("Status() = %q, want PENDING", transaction.Status())
	}
	if transaction.Origin() != OriginExternal {
		t.Fatalf("Origin() = %q, want EXTERNAL", transaction.Origin())
	}
	if transaction.ProviderID() == nil || *transaction.ProviderID() != "provider-a" {
		t.Fatal("provider id was not stored")
	}
}

func TestNewExternalKindRules(t *testing.T) {
	zero, _ := money.Zero("BRL")

	loss := validExternalInput(t)
	loss.Kind = KindLoss
	loss.Amount = zero
	if _, err := NewExternal(loss); err != nil {
		t.Fatalf("NewExternal(LOSS zero) error = %v", err)
	}

	invalidLoss := validExternalInput(t)
	invalidLoss.Kind = KindLoss
	if _, err := NewExternal(invalidLoss); !errors.Is(err, ErrInvalidTransaction) {
		t.Fatalf("NewExternal(LOSS non-zero) error = %v, want ErrInvalidTransaction", err)
	}

	betWithReference := validExternalInput(t)
	betWithReference.ReferenceExternalTransactionID = "reference-1"
	if _, err := NewExternal(betWithReference); !errors.Is(err, ErrInvalidTransaction) {
		t.Fatalf("NewExternal(BET with reference) error = %v, want ErrInvalidTransaction", err)
	}

	refundWithoutReference := validExternalInput(t)
	refundWithoutReference.Kind = KindRefund
	if _, err := NewExternal(refundWithoutReference); !errors.Is(err, ErrInvalidTransaction) {
		t.Fatalf("NewExternal(REFUND without reference) error = %v, want ErrInvalidTransaction", err)
	}

	opening := validExternalInput(t)
	opening.Kind = KindOpening
	if _, err := NewExternal(opening); !errors.Is(err, ErrInvalidTransaction) {
		t.Fatalf("NewExternal(OPENING) error = %v, want ErrInvalidTransaction", err)
	}
}

func TestMarkProcessed(t *testing.T) {
	transaction, _ := NewExternal(validExternalInput(t))
	balance, _ := money.Parse("75.00", "BRL")
	now := testNow.Add(time.Minute)

	if err := transaction.MarkProcessed(balance, now); err != nil {
		t.Fatalf("MarkProcessed() error = %v", err)
	}
	if transaction.Status() != StatusProcessed {
		t.Fatalf("Status() = %q, want PROCESSED", transaction.Status())
	}
	if transaction.ResultBalance() == nil || !transaction.ResultBalance().Equal(balance) {
		t.Fatal("result balance was not stored")
	}
	if transaction.ProcessedAt() == nil {
		t.Fatal("processedAt was not set")
	}
	if err := transaction.MarkProcessed(balance, now); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("second MarkProcessed() error = %v, want ErrInvalidTransition", err)
	}
}

func TestMarkRejected(t *testing.T) {
	transaction, _ := NewExternal(validExternalInput(t))

	if err := transaction.MarkRejected("INSUFFICIENT_FUNDS", testNow); err != nil {
		t.Fatalf("MarkRejected() error = %v", err)
	}
	if transaction.Status() != StatusRejected {
		t.Fatalf("Status() = %q, want REJECTED", transaction.Status())
	}
	if transaction.FailureCode() == nil || *transaction.FailureCode() != "INSUFFICIENT_FUNDS" {
		t.Fatal("failure code was not stored")
	}
	if transaction.ResultBalance() != nil {
		t.Fatal("rejected transaction must not have result balance")
	}
	if err := transaction.MarkProcessed(money.Money{}, testNow); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("MarkProcessed after rejection error = %v, want ErrInvalidTransition", err)
	}
}

func TestPendingReferenceLifecycle(t *testing.T) {
	input := validExternalInput(t)
	input.Kind = KindRefund
	input.ReferenceExternalTransactionID = "reference-external"
	transaction, err := NewExternal(input)
	if err != nil {
		t.Fatalf("NewExternal() error = %v", err)
	}

	referenceID := uuid.New()
	if err := transaction.SetReferenceTransactionID(referenceID); err != nil {
		t.Fatalf("SetReferenceTransactionID() error = %v", err)
	}
	if transaction.ReferenceTransactionID() == nil || *transaction.ReferenceTransactionID() != referenceID {
		t.Fatal("reference id was not stored")
	}

	next := testNow.Add(time.Second)
	if err := transaction.MarkPendingReference(next, testNow); err != nil {
		t.Fatalf("MarkPendingReference() error = %v", err)
	}
	if transaction.Status() != StatusPendingReference {
		t.Fatalf("Status() = %q, want PENDING_REFERENCE", transaction.Status())
	}
	if transaction.ReferenceAttempts() != 1 {
		t.Fatalf("ReferenceAttempts() = %d, want 1", transaction.ReferenceAttempts())
	}
	if transaction.NextReferenceAt() == nil || !transaction.NextReferenceAt().Equal(next) {
		t.Fatal("next reference time was not stored")
	}

	rescheduled := testNow.Add(2 * time.Second)
	if err := transaction.ReschedulePendingReference(rescheduled, testNow.Add(time.Second)); err != nil {
		t.Fatalf("ReschedulePendingReference() error = %v", err)
	}
	if transaction.ReferenceAttempts() != 2 {
		t.Fatalf("ReferenceAttempts() = %d, want 2", transaction.ReferenceAttempts())
	}
	if transaction.NextReferenceAt() == nil || !transaction.NextReferenceAt().Equal(rescheduled) {
		t.Fatal("rescheduled reference time was not stored")
	}

	balance, _ := money.Parse("75.00", "BRL")
	if err := transaction.MarkProcessed(balance, testNow.Add(2*time.Second)); err != nil {
		t.Fatalf("MarkProcessed() error = %v", err)
	}
	if transaction.Status() != StatusProcessed {
		t.Fatalf("Status() = %q, want PROCESSED", transaction.Status())
	}
	if transaction.NextReferenceAt() != nil {
		t.Fatal("next reference time must be cleared after processing")
	}
	if err := transaction.SetReferenceTransactionID(uuid.New()); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("SetReferenceTransactionID(terminal) error = %v, want ErrInvalidTransition", err)
	}
}

func TestPendingReferenceCanBeRejected(t *testing.T) {
	input := validExternalInput(t)
	input.Kind = KindRollback
	input.ReferenceExternalTransactionID = "reference-external"
	transaction, _ := NewExternal(input)

	if err := transaction.MarkPendingReference(testNow.Add(time.Second), testNow); err != nil {
		t.Fatalf("MarkPendingReference() error = %v", err)
	}
	if err := transaction.MarkRejected("REFERENCE_NOT_FOUND", testNow.Add(time.Minute)); err != nil {
		t.Fatalf("MarkRejected() error = %v", err)
	}
	if transaction.Status() != StatusRejected {
		t.Fatalf("Status() = %q, want REJECTED", transaction.Status())
	}
	if transaction.NextReferenceAt() != nil {
		t.Fatal("next reference time must be cleared after rejection")
	}
}

func TestRehydrateProcessedAndRejected(t *testing.T) {
	amount, _ := money.Parse("25.00", "BRL")
	result, _ := money.Parse("75.00", "BRL")
	processedAt := testNow.Add(time.Minute)

	processed, err := Rehydrate(RehydrateInput{
		ID:                    testTransactionID,
		Origin:                OriginExternal,
		Kind:                  KindBet,
		Status:                StatusProcessed,
		ProviderID:            stringPointer("provider-a"),
		ExternalTransactionID: stringPointer("external-1"),
		IdempotencyKey:        stringPointer("idem-1"),
		PayloadHash:           stringPointer("hash-1"),
		WalletID:              testWalletID,
		PlayerID:              testPlayerID,
		RoundID:               stringPointer("round-1"),
		GameID:                stringPointer("game-1"),
		Amount:                amount,
		ResultBalance:         &result,
		CreatedAt:             testNow,
		UpdatedAt:             processedAt,
		ProcessedAt:           &processedAt,
	})
	if err != nil {
		t.Fatalf("Rehydrate(processed) error = %v", err)
	}
	if processed.Status() != StatusProcessed {
		t.Fatalf("Status() = %q, want PROCESSED", processed.Status())
	}

	rejected, err := Rehydrate(RehydrateInput{
		ID:                    uuid.New(),
		Origin:                OriginExternal,
		Kind:                  KindBet,
		Status:                StatusRejected,
		ProviderID:            stringPointer("provider-a"),
		ExternalTransactionID: stringPointer("external-2"),
		IdempotencyKey:        stringPointer("idem-2"),
		PayloadHash:           stringPointer("hash-2"),
		WalletID:              testWalletID,
		PlayerID:              testPlayerID,
		RoundID:               stringPointer("round-1"),
		GameID:                stringPointer("game-1"),
		Amount:                amount,
		FailureCode:           stringPointer("INSUFFICIENT_FUNDS"),
		CreatedAt:             testNow,
		UpdatedAt:             testNow,
		ProcessedAt:           &processedAt,
	})
	if err != nil {
		t.Fatalf("Rehydrate(rejected) error = %v", err)
	}
	if rejected.Status() != StatusRejected {
		t.Fatalf("Status() = %q, want REJECTED", rejected.Status())
	}
}

func TestRehydrateRejectsInvalidState(t *testing.T) {
	amount, _ := money.Parse("25.00", "BRL")

	tests := []RehydrateInput{
		{
			ID: uuid.Nil, Origin: OriginExternal, Kind: KindBet, Status: StatusPending,
			WalletID: testWalletID, PlayerID: testPlayerID, Amount: amount,
			CreatedAt: testNow, UpdatedAt: testNow,
		},
		{
			ID: testTransactionID, Origin: OriginExternal, Kind: KindBet, Status: StatusProcessed,
			ProviderID: stringPointer("provider-a"), ExternalTransactionID: stringPointer("external-1"),
			IdempotencyKey: stringPointer("idem-1"), PayloadHash: stringPointer("hash-1"),
			WalletID: testWalletID, PlayerID: testPlayerID, RoundID: stringPointer("round-1"),
			GameID: stringPointer("game-1"), Amount: amount, CreatedAt: testNow, UpdatedAt: testNow,
		},
		{
			ID: testTransactionID, Origin: OriginExternal, Kind: KindBet, Status: StatusRejected,
			ProviderID: stringPointer("provider-a"), ExternalTransactionID: stringPointer("external-1"),
			IdempotencyKey: stringPointer("idem-1"), PayloadHash: stringPointer("hash-1"),
			WalletID: testWalletID, PlayerID: testPlayerID, RoundID: stringPointer("round-1"),
			GameID: stringPointer("game-1"), Amount: amount, CreatedAt: testNow, UpdatedAt: testNow,
			ProcessedAt: &testNow,
		},
	}

	for index, input := range tests {
		if _, err := Rehydrate(input); !errors.Is(err, ErrInvalidTransaction) {
			t.Fatalf("case %d: Rehydrate() error = %v, want ErrInvalidTransaction", index, err)
		}
	}
}
