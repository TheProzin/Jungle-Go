package wagering

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/TheProzin/Jungle-Go/internal/domain/money"
)

type Fingerprint struct {
	ProviderID                     string
	ExternalTransactionID          string
	PlayerID                       string
	WalletID                       string
	RoundID                        string
	GameID                         string
	Kind                           Kind
	Amount                         money.Money
	ReferenceExternalTransactionID string
}

type canonicalMoney struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

type canonicalPayload struct {
	ExternalTransactionID          string         `json:"externalTransactionId"`
	GameID                         string         `json:"gameId"`
	Kind                           string         `json:"kind"`
	Money                          canonicalMoney `json:"money"`
	PlayerID                       string         `json:"playerId"`
	ProviderID                     string         `json:"providerId"`
	ReferenceExternalTransactionID string         `json:"referenceExternalTransactionId,omitempty"`
	RoundID                        string         `json:"roundId"`
	WalletID                       string         `json:"walletId"`
}

func (f Fingerprint) Hash() (string, error) {
	if f.ProviderID == "" || f.ExternalTransactionID == "" ||
		f.PlayerID == "" || f.WalletID == "" || f.RoundID == "" || f.GameID == "" {
		return "", fmt.Errorf("%w: fingerprint fields are required", ErrInvalidTransaction)
	}
	if f.Kind == KindOpening || !validKind(f.Kind) {
		return "", fmt.Errorf("%w: invalid kind %q", ErrInvalidTransaction, f.Kind)
	}
	if !f.Amount.IsValid() {
		return "", fmt.Errorf("%w: invalid amount", ErrInvalidTransaction)
	}

	payload := canonicalPayload{
		ExternalTransactionID: f.ExternalTransactionID,
		GameID:                f.GameID,
		Kind:                  string(f.Kind),
		Money: canonicalMoney{
			Amount:   f.Amount.Amount(),
			Currency: f.Amount.Currency(),
		},
		PlayerID:                       f.PlayerID,
		ProviderID:                     f.ProviderID,
		ReferenceExternalTransactionID: f.ReferenceExternalTransactionID,
		RoundID:                        f.RoundID,
		WalletID:                       f.WalletID,
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal fingerprint: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}
