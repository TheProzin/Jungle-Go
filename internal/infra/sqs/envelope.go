package sqs

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	wageringapp "github.com/TheProzin/Jungle-Go/internal/app/wagering"
	"github.com/TheProzin/Jungle-Go/internal/domain/money"
	domainwagering "github.com/TheProzin/Jungle-Go/internal/domain/wagering"
)

var ErrInvalidMessage = errors.New("invalid sqs message")

const WagerTransactionRequested = "WagerTransactionRequested"

type Envelope struct {
	MessageID  string       `json:"messageId"`
	Type       string       `json:"type"`
	OccurredAt time.Time    `json:"occurredAt"`
	Data       EnvelopeData `json:"data"`
}

type EnvelopeData struct {
	ProviderID                     string      `json:"providerId"`
	ExternalTransactionID          string      `json:"externalTransactionId"`
	IdempotencyKey                 string      `json:"idempotencyKey"`
	PlayerID                       string      `json:"playerId"`
	WalletID                       string      `json:"walletId"`
	RoundID                        string      `json:"roundId"`
	GameID                         string      `json:"gameId"`
	Kind                           string      `json:"kind"`
	Money                          money.Money `json:"money"`
	ReferenceExternalTransactionID string      `json:"referenceExternalTransactionId,omitempty"`
}

func ParseEnvelope(body []byte) (*Envelope, error) {
	var envelope Envelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidMessage, err)
	}

	if envelope.MessageID == "" {
		return nil, fmt.Errorf("%w: messageId is required", ErrInvalidMessage)
	}
	if envelope.Type != WagerTransactionRequested {
		return nil, fmt.Errorf("%w: unsupported type %q", ErrInvalidMessage, envelope.Type)
	}
	if envelope.OccurredAt.IsZero() {
		return nil, fmt.Errorf("%w: occurredAt is required", ErrInvalidMessage)
	}
	if envelope.Data.ProviderID == "" || envelope.Data.ExternalTransactionID == "" ||
		envelope.Data.IdempotencyKey == "" || envelope.Data.PlayerID == "" ||
		envelope.Data.WalletID == "" || envelope.Data.RoundID == "" ||
		envelope.Data.GameID == "" || envelope.Data.Kind == "" {
		return nil, fmt.Errorf("%w: required data fields are missing", ErrInvalidMessage)
	}
	if len(envelope.Data.IdempotencyKey) > 255 {
		return nil, fmt.Errorf("%w: idempotency key is too long", ErrInvalidMessage)
	}

	return &envelope, nil
}

func (e *Envelope) Command() (wageringapp.ProcessCommand, error) {
	playerID, err := uuid.Parse(e.Data.PlayerID)
	if err != nil {
		return wageringapp.ProcessCommand{}, fmt.Errorf("%w: invalid playerId", ErrInvalidMessage)
	}
	walletID, err := uuid.Parse(e.Data.WalletID)
	if err != nil {
		return wageringapp.ProcessCommand{}, fmt.Errorf("%w: invalid walletId", ErrInvalidMessage)
	}

	kind := domainwagering.Kind(e.Data.Kind)
	switch kind {
	case domainwagering.KindBet, domainwagering.KindWin:
		if !e.Data.Money.IsPositive() {
			return wageringapp.ProcessCommand{}, fmt.Errorf("%w: amount must be positive", ErrInvalidMessage)
		}
	case domainwagering.KindLoss:
		if !e.Data.Money.IsZero() {
			return wageringapp.ProcessCommand{}, fmt.Errorf("%w: loss amount must be zero", ErrInvalidMessage)
		}
	case domainwagering.KindRefund, domainwagering.KindRollback:
		if !e.Data.Money.IsPositive() {
			return wageringapp.ProcessCommand{}, fmt.Errorf("%w: amount must be positive", ErrInvalidMessage)
		}
		if e.Data.ReferenceExternalTransactionID == "" {
			return wageringapp.ProcessCommand{}, fmt.Errorf("%w: reference is required", ErrInvalidMessage)
		}
	case domainwagering.KindOpening:
		return wageringapp.ProcessCommand{}, fmt.Errorf("%w: opening is not allowed", ErrInvalidMessage)
	default:
		return wageringapp.ProcessCommand{}, fmt.Errorf("%w: invalid kind %q", ErrInvalidMessage, e.Data.Kind)
	}

	if (kind == domainwagering.KindBet || kind == domainwagering.KindLoss) &&
		e.Data.ReferenceExternalTransactionID != "" {
		return wageringapp.ProcessCommand{}, fmt.Errorf("%w: reference is not allowed for %s", ErrInvalidMessage, kind)
	}

	return wageringapp.ProcessCommand{
		ProviderID:                     e.Data.ProviderID,
		ExternalTransactionID:          e.Data.ExternalTransactionID,
		IdempotencyKey:                 e.Data.IdempotencyKey,
		PlayerID:                       playerID,
		WalletID:                       walletID,
		RoundID:                        e.Data.RoundID,
		GameID:                         e.Data.GameID,
		Kind:                           kind,
		Amount:                         e.Data.Money,
		ReferenceExternalTransactionID: e.Data.ReferenceExternalTransactionID,
		CorrelationID:                  e.MessageID,
	}, nil
}
