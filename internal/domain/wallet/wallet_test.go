package wallet

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/TheProzin/Jungle-Go/internal/domain/money"
)

var (
	testWalletID = uuid.MustParse("11111111-1111-7111-8111-111111111111")
	testPlayerID = uuid.MustParse("22222222-2222-7222-8222-222222222222")
	testNow      = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
)

func TestNew(t *testing.T) {
	initial, _ := money.Parse("100.00", "BRL")

	created, err := New(testWalletID, testPlayerID, initial, testNow)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if created.Version() != 1 {
		t.Fatalf("Version() = %d, want 1", created.Version())
	}
	if !created.Balance().Equal(initial) {
		t.Fatalf("Balance() = %s, want %s", created.Balance().Amount(), initial.Amount())
	}
	if created.CreatedAt() != testNow || created.UpdatedAt() != testNow {
		t.Fatal("timestamps were not preserved")
	}
}

func TestNewRejectsInvalidValues(t *testing.T) {
	valid, _ := money.Parse("1.00", "BRL")
	negative, _ := money.New(-1, "BRL")

	tests := []struct {
		name    string
		id      uuid.UUID
		player  uuid.UUID
		balance money.Money
		now     time.Time
		want    error
	}{
		{name: "nil id", id: uuid.Nil, player: testPlayerID, balance: valid, now: testNow, want: ErrInvalidWallet},
		{name: "nil player", id: testWalletID, player: uuid.Nil, balance: valid, now: testNow, want: ErrInvalidWallet},
		{name: "negative balance", id: testWalletID, player: testPlayerID, balance: negative, now: testNow, want: ErrNegativeInitialBalance},
		{name: "zero time", id: testWalletID, player: testPlayerID, balance: valid, now: time.Time{}, want: ErrInvalidWallet},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := New(test.id, test.player, test.balance, test.now)
			if !errors.Is(err, test.want) {
				t.Fatalf("New() error = %v, want %v", err, test.want)
			}
		})
	}

	var zero money.Money
	if _, err := New(testWalletID, testPlayerID, zero, testNow); !errors.Is(err, ErrInvalidWallet) {
		t.Fatalf("New(zero value money) error = %v, want ErrInvalidWallet", err)
	}
}

func TestDebitAndCredit(t *testing.T) {
	initial, _ := money.Parse("100.00", "BRL")
	created, _ := New(testWalletID, testPlayerID, initial, testNow)
	debit, _ := money.Parse("30.00", "BRL")
	credit, _ := money.Parse("10.00", "BRL")

	if err := created.Debit(debit, testNow.Add(time.Minute)); err != nil {
		t.Fatalf("Debit() error = %v", err)
	}
	if created.Balance().Amount() != "70.00" {
		t.Fatalf("Balance() = %s, want 70.00", created.Balance().Amount())
	}
	if created.Version() != 2 {
		t.Fatalf("Version() = %d, want 2", created.Version())
	}

	if err := created.Credit(credit, testNow.Add(2*time.Minute)); err != nil {
		t.Fatalf("Credit() error = %v", err)
	}
	if created.Balance().Amount() != "80.00" {
		t.Fatalf("Balance() = %s, want 80.00", created.Balance().Amount())
	}
	if created.Version() != 3 {
		t.Fatalf("Version() = %d, want 3", created.Version())
	}
}

func TestDebitRejectsInvalidAmounts(t *testing.T) {
	initial, _ := money.Parse("100.00", "BRL")
	created, _ := New(testWalletID, testPlayerID, initial, testNow)
	tooMuch, _ := money.Parse("100.01", "BRL")
	usd, _ := money.Parse("1.00", "USD")
	zero, _ := money.Zero("BRL")

	if err := created.Debit(tooMuch, testNow); !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("Debit(too much) error = %v, want ErrInsufficientFunds", err)
	}
	if err := created.Debit(usd, testNow); !errors.Is(err, money.ErrCurrencyMismatch) {
		t.Fatalf("Debit(USD) error = %v, want ErrCurrencyMismatch", err)
	}
	if err := created.Debit(zero, testNow); !errors.Is(err, ErrNonPositiveAmount) {
		t.Fatalf("Debit(zero) error = %v, want ErrNonPositiveAmount", err)
	}
}

func TestCreditRejectsCurrencyMismatch(t *testing.T) {
	initial, _ := money.Parse("100.00", "BRL")
	created, _ := New(testWalletID, testPlayerID, initial, testNow)
	usd, _ := money.Parse("1.00", "USD")

	if err := created.Credit(usd, testNow); !errors.Is(err, money.ErrCurrencyMismatch) {
		t.Fatalf("Credit(USD) error = %v, want ErrCurrencyMismatch", err)
	}
}

func TestRehydrate(t *testing.T) {
	balance, _ := money.Parse("10.00", "BRL")

	rehydrated, err := Rehydrate(testWalletID, testPlayerID, balance, 2, testNow, testNow)
	if err != nil {
		t.Fatalf("Rehydrate() error = %v", err)
	}
	if rehydrated.Version() != 2 {
		t.Fatalf("Version() = %d, want 2", rehydrated.Version())
	}

	negative, _ := money.New(-1, "BRL")
	if _, err := Rehydrate(testWalletID, testPlayerID, negative, 2, testNow, testNow); !errors.Is(err, ErrInvalidWallet) {
		t.Fatalf("Rehydrate(negative) error = %v, want ErrInvalidWallet", err)
	}
	if _, err := Rehydrate(uuid.Nil, testPlayerID, balance, 2, testNow, testNow); !errors.Is(err, ErrInvalidWallet) {
		t.Fatalf("Rehydrate(nil id) error = %v, want ErrInvalidWallet", err)
	}
}
