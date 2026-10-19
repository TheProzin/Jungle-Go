package wagering

import (
	"errors"
	"testing"

	"github.com/TheProzin/Jungle-Go/internal/domain/money"
)

func baseFingerprint(t *testing.T) Fingerprint {
	t.Helper()
	amount, _ := money.Parse("25.00", "BRL")
	return Fingerprint{
		ProviderID:            "provider-a",
		ExternalTransactionID: "transaction-1",
		PlayerID:              "player-1",
		WalletID:              "wallet-1",
		RoundID:               "round-1",
		GameID:                "game-1",
		Kind:                  KindBet,
		Amount:                amount,
	}
}

func TestFingerprintHashIsDeterministic(t *testing.T) {
	fingerprint := baseFingerprint(t)

	first, err := fingerprint.Hash()
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	second, err := fingerprint.Hash()
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	if first != second {
		t.Fatalf("Hash() = %q and %q, want equal", first, second)
	}
	if len(first) != 64 {
		t.Fatalf("hash length = %d, want 64", len(first))
	}
}

func TestFingerprintUsesNormalizedAmount(t *testing.T) {
	fromString, _ := money.Parse("25.00", "BRL")
	fromMinor, _ := money.New(2500, "BRL")

	first := baseFingerprint(t)
	first.Amount = fromString
	second := baseFingerprint(t)
	second.Amount = fromMinor

	firstHash, _ := first.Hash()
	secondHash, _ := second.Hash()
	if firstHash != secondHash {
		t.Fatalf("normalized amounts produced different hashes")
	}
}

func TestFingerprintChangesWithBusinessFields(t *testing.T) {
	base := baseFingerprint(t)
	baseHash, _ := base.Hash()

	tests := []struct {
		name   string
		mutate func(*Fingerprint)
	}{
		{name: "external id", mutate: func(f *Fingerprint) { f.ExternalTransactionID = "transaction-2" }},
		{name: "player", mutate: func(f *Fingerprint) { f.PlayerID = "player-2" }},
		{name: "wallet", mutate: func(f *Fingerprint) { f.WalletID = "wallet-2" }},
		{name: "round", mutate: func(f *Fingerprint) { f.RoundID = "round-2" }},
		{name: "game", mutate: func(f *Fingerprint) { f.GameID = "game-2" }},
		{name: "kind", mutate: func(f *Fingerprint) { f.Kind = KindWin }},
		{name: "amount", mutate: func(f *Fingerprint) {
			amount, _ := money.Parse("25.01", "BRL")
			f.Amount = amount
		}},
		{name: "reference", mutate: func(f *Fingerprint) { f.ReferenceExternalTransactionID = "reference-1" }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := base
			test.mutate(&changed)
			changedHash, err := changed.Hash()
			if err != nil {
				t.Fatalf("Hash() error = %v", err)
			}
			if changedHash == baseHash {
				t.Fatal("hash did not change")
			}
		})
	}
}

func TestFingerprintRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Fingerprint)
	}{
		{name: "empty provider", mutate: func(f *Fingerprint) { f.ProviderID = "" }},
		{name: "empty external id", mutate: func(f *Fingerprint) { f.ExternalTransactionID = "" }},
		{name: "empty player", mutate: func(f *Fingerprint) { f.PlayerID = "" }},
		{name: "empty wallet", mutate: func(f *Fingerprint) { f.WalletID = "" }},
		{name: "empty round", mutate: func(f *Fingerprint) { f.RoundID = "" }},
		{name: "empty game", mutate: func(f *Fingerprint) { f.GameID = "" }},
		{name: "opening kind", mutate: func(f *Fingerprint) { f.Kind = KindOpening }},
		{name: "invalid money", mutate: func(f *Fingerprint) { f.Amount = money.Money{} }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fingerprint := baseFingerprint(t)
			test.mutate(&fingerprint)
			if _, err := fingerprint.Hash(); !errors.Is(err, ErrInvalidTransaction) {
				t.Fatalf("Hash() error = %v, want ErrInvalidTransaction", err)
			}
		})
	}
}
