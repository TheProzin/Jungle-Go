package ledger

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/TheProzin/Jungle-Go/internal/domain/money"
)

var (
	testEntryID       = uuid.MustParse("11111111-1111-7111-8111-111111111111")
	testWalletID      = uuid.MustParse("22222222-2222-7222-8222-222222222222")
	testTransactionID = uuid.MustParse("33333333-3333-7333-8333-333333333333")
	testNow           = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
)

func TestNewCredit(t *testing.T) {
	amount, _ := money.Parse("10.00", "BRL")
	before, _ := money.Parse("90.00", "BRL")
	after, _ := money.Parse("100.00", "BRL")

	entry, err := NewCredit(testEntryID, testWalletID, testTransactionID, amount, before, after, testNow)
	if err != nil {
		t.Fatalf("NewCredit() error = %v", err)
	}
	if entry.Direction() != DirectionCredit {
		t.Fatalf("Direction() = %q, want CREDIT", entry.Direction())
	}
	if !entry.BalanceAfter().Equal(after) {
		t.Fatalf("BalanceAfter() = %s, want %s", entry.BalanceAfter().Amount(), after.Amount())
	}
}

func TestNewDebit(t *testing.T) {
	amount, _ := money.Parse("10.00", "BRL")
	before, _ := money.Parse("100.00", "BRL")
	after, _ := money.Parse("90.00", "BRL")

	entry, err := NewDebit(testEntryID, testWalletID, testTransactionID, amount, before, after, testNow)
	if err != nil {
		t.Fatalf("NewDebit() error = %v", err)
	}
	if entry.Direction() != DirectionDebit {
		t.Fatalf("Direction() = %q, want DEBIT", entry.Direction())
	}
}

func TestEntryRejectsInvalidBalanceMath(t *testing.T) {
	amount, _ := money.Parse("10.00", "BRL")
	before, _ := money.Parse("100.00", "BRL")
	wrongAfter, _ := money.Parse("95.00", "BRL")

	if _, err := NewDebit(testEntryID, testWalletID, testTransactionID, amount, before, wrongAfter, testNow); !errors.Is(err, ErrBalanceMismatch) {
		t.Fatalf("NewDebit() error = %v, want ErrBalanceMismatch", err)
	}
}

func TestEntryRejectsDebitAboveBalance(t *testing.T) {
	amount, _ := money.Parse("100.00", "BRL")
	before, _ := money.Parse("50.00", "BRL")
	after, _ := money.Parse("0.00", "BRL")

	if _, err := NewDebit(testEntryID, testWalletID, testTransactionID, amount, before, after, testNow); !errors.Is(err, ErrBalanceMismatch) {
		t.Fatalf("NewDebit() error = %v, want ErrBalanceMismatch", err)
	}
}

func TestEntryRejectsCurrencyMismatch(t *testing.T) {
	amount, _ := money.Parse("10.00", "USD")
	before, _ := money.Parse("100.00", "BRL")
	after, _ := money.Parse("90.00", "BRL")

	if _, err := NewDebit(testEntryID, testWalletID, testTransactionID, amount, before, after, testNow); !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("NewDebit() error = %v, want ErrInvalidEntry", err)
	}
}

func TestEntryRejectsInvalidValues(t *testing.T) {
	amount, _ := money.Parse("10.00", "BRL")
	before, _ := money.Parse("100.00", "BRL")
	after, _ := money.Parse("90.00", "BRL")
	zero, _ := money.Zero("BRL")

	if _, err := NewDebit(uuid.Nil, testWalletID, testTransactionID, amount, before, after, testNow); !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("NewDebit(nil id) error = %v, want ErrInvalidEntry", err)
	}
	if _, err := NewDebit(testEntryID, testWalletID, testTransactionID, zero, before, after, testNow); !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("NewDebit(zero amount) error = %v, want ErrInvalidEntry", err)
	}
	if _, err := NewDebit(testEntryID, testWalletID, testTransactionID, amount, before, after, time.Time{}); !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("NewDebit(zero time) error = %v, want ErrInvalidEntry", err)
	}
}

func TestRehydrate(t *testing.T) {
	amount, _ := money.Parse("10.00", "BRL")
	before, _ := money.Parse("100.00", "BRL")
	after, _ := money.Parse("90.00", "BRL")

	entry, err := Rehydrate(testEntryID, testWalletID, testTransactionID, DirectionDebit, amount, before, after, testNow)
	if err != nil {
		t.Fatalf("Rehydrate() error = %v", err)
	}
	if entry.ID() != testEntryID {
		t.Fatalf("ID() = %s, want %s", entry.ID(), testEntryID)
	}
}
