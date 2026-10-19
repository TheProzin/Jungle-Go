package sqs

import (
	"errors"
	"testing"

	domainwagering "github.com/TheProzin/Jungle-Go/internal/domain/wagering"
)

const validEnvelope = `{
  "messageId": "msg-123",
  "type": "WagerTransactionRequested",
  "occurredAt": "2026-09-08T12:00:00.000Z",
  "data": {
    "providerId": "provider-a",
    "externalTransactionId": "transaction-123",
    "idempotencyKey": "provider-a:transaction-123",
    "playerId": "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1",
    "walletId": "0192f291-27dd-7d3f-8071-5f8685deef37",
    "roundId": "round-987",
    "gameId": "fortune-chimp",
    "kind": "BET",
    "money": { "amount": "25.00", "currency": "BRL" }
  }
}`

func TestParseEnvelopeAndCommand(t *testing.T) {
	envelope, err := ParseEnvelope([]byte(validEnvelope))
	if err != nil {
		t.Fatalf("ParseEnvelope() error = %v", err)
	}
	if envelope.MessageID != "msg-123" {
		t.Fatalf("MessageID = %q, want msg-123", envelope.MessageID)
	}

	command, err := envelope.Command()
	if err != nil {
		t.Fatalf("Command() error = %v", err)
	}
	if command.ProviderID != "provider-a" || command.ExternalTransactionID != "transaction-123" {
		t.Fatalf("unexpected command: %+v", command)
	}
	if command.Kind != domainwagering.KindBet {
		t.Fatalf("Kind = %q, want BET", command.Kind)
	}
	if command.Amount.Amount() != "25.00" {
		t.Fatalf("Amount = %s, want 25.00", command.Amount.Amount())
	}
	if command.CorrelationID != "msg-123" {
		t.Fatalf("CorrelationID = %q, want msg-123", command.CorrelationID)
	}
}

func TestParseEnvelopeRejectsInvalidMessages(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "invalid json", body: `{`},
		{name: "missing message id", body: `{"type":"WagerTransactionRequested","occurredAt":"2026-09-08T12:00:00Z","data":{}}`},
		{name: "invalid type", body: `{"messageId":"msg-1","type":"Other","occurredAt":"2026-09-08T12:00:00Z","data":{}}`},
		{name: "missing occurred at", body: `{"messageId":"msg-1","type":"WagerTransactionRequested","data":{}}`},
		{name: "missing data", body: `{"messageId":"msg-1","type":"WagerTransactionRequested","occurredAt":"2026-09-08T12:00:00Z","data":{}}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParseEnvelope([]byte(test.body)); !errors.Is(err, ErrInvalidMessage) {
				t.Fatalf("ParseEnvelope() error = %v, want ErrInvalidMessage", err)
			}
		})
	}
}

func TestEnvelopeCommandRejectsInvalidBusinessData(t *testing.T) {
	tests := []struct {
		name      string
		kind      string
		amount    string
		reference string
	}{
		{name: "loss with non zero amount", kind: "LOSS", amount: "1.00"},
		{name: "refund without reference", kind: "REFUND", amount: "25.00"},
		{name: "opening", kind: "OPENING", amount: "25.00"},
		{name: "unknown kind", kind: "OTHER", amount: "25.00"},
		{name: "bet with reference", kind: "BET", amount: "25.00", reference: "bet-1"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := `{
				"messageId":"msg-invalid","type":"WagerTransactionRequested",
				"occurredAt":"2026-09-08T12:00:00Z",
				"data":{
					"providerId":"provider-a","externalTransactionId":"transaction-x",
					"idempotencyKey":"idem-x",
					"playerId":"0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1",
					"walletId":"0192f291-27dd-7d3f-8071-5f8685deef37",
					"roundId":"round-1","gameId":"game-1","kind":"` + test.kind + `",
					"money":{"amount":"` + test.amount + `","currency":"BRL"}`
			if test.reference != "" {
				body += `,"referenceExternalTransactionId":"` + test.reference + `"`
			}
			body += `}}`

			envelope, err := ParseEnvelope([]byte(body))
			if err != nil {
				t.Fatalf("ParseEnvelope() error = %v", err)
			}
			if _, err := envelope.Command(); !errors.Is(err, ErrInvalidMessage) {
				t.Fatalf("Command() error = %v, want ErrInvalidMessage", err)
			}
		})
	}
}

func TestEnvelopeCommandRejectsInvalidIDs(t *testing.T) {
	body := `{
		"messageId":"msg-invalid-ids","type":"WagerTransactionRequested",
		"occurredAt":"2026-09-08T12:00:00Z",
		"data":{
			"providerId":"provider-a","externalTransactionId":"transaction-x",
			"idempotencyKey":"idem-x","playerId":"not-a-uuid",
			"walletId":"0192f291-27dd-7d3f-8071-5f8685deef37",
			"roundId":"round-1","gameId":"game-1","kind":"BET",
			"money":{"amount":"25.00","currency":"BRL"}
		}
	}`

	envelope, err := ParseEnvelope([]byte(body))
	if err != nil {
		t.Fatalf("ParseEnvelope() error = %v", err)
	}
	if _, err := envelope.Command(); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("Command() error = %v, want ErrInvalidMessage", err)
	}
}

func TestEnvelopeCommandAllowsRefundReference(t *testing.T) {
	body := `{
		"messageId":"msg-refund","type":"WagerTransactionRequested",
		"occurredAt":"2026-09-08T12:00:00Z",
		"data":{
			"providerId":"provider-a","externalTransactionId":"refund-1",
			"idempotencyKey":"idem-refund-1",
			"playerId":"0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1",
			"walletId":"0192f291-27dd-7d3f-8071-5f8685deef37",
			"roundId":"round-1","gameId":"game-1","kind":"REFUND",
			"money":{"amount":"25.00","currency":"BRL"},
			"referenceExternalTransactionId":"bet-1"
		}
	}`

	envelope, err := ParseEnvelope([]byte(body))
	if err != nil {
		t.Fatalf("ParseEnvelope() error = %v", err)
	}
	command, err := envelope.Command()
	if err != nil {
		t.Fatalf("Command() error = %v", err)
	}
	if command.ReferenceExternalTransactionID != "bet-1" {
		t.Fatalf("ReferenceExternalTransactionID = %q, want bet-1", command.ReferenceExternalTransactionID)
	}
}
