package events

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/TheProzin/Jungle-Go/internal/domain/ledger"
	"github.com/TheProzin/Jungle-Go/internal/domain/money"
)

var (
	testEventID = uuid.MustParse("11111111-1111-7111-8111-111111111111")
	testNow     = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
)

func TestNewWagerTransactionProcessed(t *testing.T) {
	amount, _ := money.Parse("100.00", "BRL")

	event, err := NewWagerTransactionProcessed(
		testEventID,
		"transaction-1",
		"correlation-1",
		nil,
		testNow,
		WagerTransactionProcessedData{
			TransactionID: "transaction-1",
			Kind:          "OPENING",
			WalletID:      "wallet-1",
			PlayerID:      "player-1",
			Money:         amount,
			Status:        "PROCESSED",
		},
	)
	if err != nil {
		t.Fatalf("NewWagerTransactionProcessed() error = %v", err)
	}
	if event.Type() != TypeWagerTransactionProcessed {
		t.Fatalf("Type() = %q, want %q", event.Type(), TypeWagerTransactionProcessed)
	}
	if event.Version() != EventVersion {
		t.Fatalf("Version() = %d, want %d", event.Version(), EventVersion)
	}
	if event.AggregateType() != AggregateWagerTransaction {
		t.Fatalf("AggregateType() = %q, want %q", event.AggregateType(), AggregateWagerTransaction)
	}
}

func TestNewWagerTransactionProcessedRejectsInvalidMoney(t *testing.T) {
	var zero money.Money

	_, err := NewWagerTransactionProcessed(
		testEventID,
		"transaction-1",
		"correlation-1",
		nil,
		testNow,
		WagerTransactionProcessedData{Money: zero},
	)
	if !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("error = %v, want ErrInvalidEvent", err)
	}
}

func TestNewWalletBalanceChanged(t *testing.T) {
	amount, _ := money.Parse("100.00", "BRL")
	before, _ := money.Zero("BRL")

	event, err := NewWalletBalanceChanged(
		testEventID,
		"wallet-1",
		"correlation-1",
		nil,
		testNow,
		WalletBalanceChangedData{
			WalletID:      "wallet-1",
			TransactionID: "transaction-1",
			Direction:     ledger.DirectionCredit,
			Money:         amount,
			BalanceBefore: before,
			BalanceAfter:  amount,
			WalletVersion: 1,
		},
	)
	if err != nil {
		t.Fatalf("NewWalletBalanceChanged() error = %v", err)
	}
	if event.Type() != TypeWalletBalanceChanged {
		t.Fatalf("Type() = %q, want %q", event.Type(), TypeWalletBalanceChanged)
	}
	if event.AggregateType() != AggregateWallet {
		t.Fatalf("AggregateType() = %q, want %q", event.AggregateType(), AggregateWallet)
	}
}

func TestNewWalletBalanceChangedRejectsInvalidData(t *testing.T) {
	amount, _ := money.Parse("100.00", "BRL")
	before, _ := money.Zero("BRL")

	tests := []struct {
		name string
		data WalletBalanceChangedData
	}{
		{
			name: "invalid direction",
			data: WalletBalanceChangedData{
				Direction:     "OTHER",
				Money:         amount,
				BalanceBefore: before,
				BalanceAfter:  amount,
				WalletVersion: 1,
			},
		},
		{
			name: "zero money",
			data: WalletBalanceChangedData{
				Direction:     ledger.DirectionCredit,
				Money:         before,
				BalanceBefore: before,
				BalanceAfter:  before,
				WalletVersion: 1,
			},
		},
		{
			name: "invalid version",
			data: WalletBalanceChangedData{
				Direction:     ledger.DirectionCredit,
				Money:         amount,
				BalanceBefore: before,
				BalanceAfter:  amount,
				WalletVersion: 0,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewWalletBalanceChanged(testEventID, "wallet-1", "correlation-1", nil, testNow, test.data)
			if !errors.Is(err, ErrInvalidEvent) {
				t.Fatalf("error = %v, want ErrInvalidEvent", err)
			}
		})
	}
}

func TestEventRequiresIdentity(t *testing.T) {
	amount, _ := money.Parse("100.00", "BRL")

	if _, err := NewWagerTransactionProcessed(uuid.Nil, "aggregate", "correlation", nil, testNow, WagerTransactionProcessedData{Money: amount}); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("nil event id error = %v, want ErrInvalidEvent", err)
	}
	if _, err := NewWagerTransactionProcessed(testEventID, "", "correlation", nil, testNow, WagerTransactionProcessedData{Money: amount}); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("empty aggregate error = %v, want ErrInvalidEvent", err)
	}
	if _, err := NewWagerTransactionProcessed(testEventID, "aggregate", "", nil, testNow, WagerTransactionProcessedData{Money: amount}); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("empty correlation error = %v, want ErrInvalidEvent", err)
	}
	if _, err := NewWagerTransactionProcessed(testEventID, "aggregate", "correlation", nil, time.Time{}, WagerTransactionProcessedData{Money: amount}); !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("zero time error = %v, want ErrInvalidEvent", err)
	}
}
