package wagering

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/TheProzin/Jungle-Go/internal/domain/money"
)

var (
	testTransactionID = uuid.MustParse("11111111-1111-7111-8111-111111111111")
	testWalletID      = uuid.MustParse("22222222-2222-7222-8222-222222222222")
	testPlayerID      = uuid.MustParse("33333333-3333-7333-8333-333333333333")
	testNow           = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
)

func TestNewOpening(t *testing.T) {
	amount, _ := money.Parse("100.00", "BRL")

	opening, err := NewOpening(testTransactionID, "opening-1", testWalletID, testPlayerID, amount, testNow)
	if err != nil {
		t.Fatalf("NewOpening() error = %v", err)
	}
	if opening.Origin() != OriginInternal {
		t.Fatalf("Origin() = %q, want INTERNAL", opening.Origin())
	}
	if opening.Kind() != KindOpening {
		t.Fatalf("Kind() = %q, want OPENING", opening.Kind())
	}
	if opening.Status() != StatusProcessed {
		t.Fatalf("Status() = %q, want PROCESSED", opening.Status())
	}
	if opening.ProviderID() != nil || opening.ExternalTransactionID() != nil {
		t.Fatal("opening must not have external metadata")
	}
	if opening.ResultBalance() == nil || !opening.ResultBalance().Equal(amount) {
		t.Fatal("result balance must equal opening amount")
	}
	if opening.ProcessedAt() == nil {
		t.Fatal("processedAt must be set")
	}
	if !opening.IsTerminal() {
		t.Fatal("processed opening must be terminal")
	}
}

func TestNewOpeningRejectsInvalidValues(t *testing.T) {
	amount, _ := money.Parse("100.00", "BRL")
	zero, _ := money.Zero("BRL")
	negative, _ := money.New(-1, "BRL")

	tests := []struct {
		name      string
		id        uuid.UUID
		reference string
		walletID  uuid.UUID
		playerID  uuid.UUID
		amount    money.Money
		now       time.Time
	}{
		{name: "nil id", id: uuid.Nil, reference: "ref", walletID: testWalletID, playerID: testPlayerID, amount: amount, now: testNow},
		{name: "empty reference", id: testTransactionID, reference: "", walletID: testWalletID, playerID: testPlayerID, amount: amount, now: testNow},
		{name: "nil wallet", id: testTransactionID, reference: "ref", walletID: uuid.Nil, playerID: testPlayerID, amount: amount, now: testNow},
		{name: "nil player", id: testTransactionID, reference: "ref", walletID: testWalletID, playerID: uuid.Nil, amount: amount, now: testNow},
		{name: "zero amount", id: testTransactionID, reference: "ref", walletID: testWalletID, playerID: testPlayerID, amount: zero, now: testNow},
		{name: "negative amount", id: testTransactionID, reference: "ref", walletID: testWalletID, playerID: testPlayerID, amount: negative, now: testNow},
		{name: "zero time", id: testTransactionID, reference: "ref", walletID: testWalletID, playerID: testPlayerID, amount: amount, now: time.Time{}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewOpening(test.id, test.reference, test.walletID, test.playerID, test.amount, test.now); !errors.Is(err, ErrInvalidTransaction) {
				t.Fatalf("NewOpening() error = %v, want ErrInvalidTransaction", err)
			}
		})
	}
}
