package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	wageringapp "github.com/TheProzin/Jungle-Go/internal/app/wagering"
	"github.com/TheProzin/Jungle-Go/internal/domain/money"
	domainwagering "github.com/TheProzin/Jungle-Go/internal/domain/wagering"
	domainwallet "github.com/TheProzin/Jungle-Go/internal/domain/wallet"
)

type WageringHandler struct {
	service *wageringapp.Service
}

type processTransactionRequest struct {
	ProviderID                     string      `json:"providerId"`
	ExternalTransactionID          string      `json:"externalTransactionId"`
	PlayerID                       string      `json:"playerId"`
	WalletID                       string      `json:"walletId"`
	RoundID                        string      `json:"roundId"`
	GameID                         string      `json:"gameId"`
	Kind                           string      `json:"kind"`
	Money                          money.Money `json:"money"`
	ReferenceExternalTransactionID string      `json:"referenceExternalTransactionId,omitempty"`
}

type processTransactionResponse struct {
	TransactionID    string       `json:"transactionId"`
	Status           string       `json:"status"`
	Balance          *money.Money `json:"balance,omitempty"`
	FailureCode      string       `json:"failureCode,omitempty"`
	IdempotentReplay bool         `json:"idempotentReplay"`
}

type transactionResponse struct {
	TransactionID                  string       `json:"transactionId"`
	ProviderID                     string       `json:"providerId,omitempty"`
	ExternalTransactionID          string       `json:"externalTransactionId,omitempty"`
	WalletID                       string       `json:"walletId"`
	PlayerID                       string       `json:"playerId"`
	RoundID                        string       `json:"roundId,omitempty"`
	GameID                         string       `json:"gameId,omitempty"`
	Kind                           string       `json:"kind"`
	Status                         string       `json:"status"`
	FailureCode                    string       `json:"failureCode,omitempty"`
	Money                          money.Money  `json:"money"`
	Balance                        *money.Money `json:"balance,omitempty"`
	ReferenceExternalTransactionID string       `json:"referenceExternalTransactionId,omitempty"`
	ReferenceTransactionID         string       `json:"referenceTransactionId,omitempty"`
	ReferenceAttempts              int          `json:"referenceAttempts"`
	NextReferenceAt                *time.Time   `json:"nextReferenceAt,omitempty"`
	CreatedAt                      time.Time    `json:"createdAt"`
	ProcessedAt                    *time.Time   `json:"processedAt,omitempty"`
}

func NewWageringHandler(service *wageringapp.Service) *WageringHandler {
	return &WageringHandler{service: service}
}

func (h *WageringHandler) Process(w http.ResponseWriter, r *http.Request) {
	var request processTransactionRequest
	if err := decodeJSON(w, r, &request); err != nil {
		if errors.Is(err, money.ErrInvalidAmount) || errors.Is(err, money.ErrInvalidCurrency) {
			writeAPIError(w, http.StatusBadRequest, "INVALID_MONEY", "invalid money")
			return
		}
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey == "" {
		writeAPIError(w, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED", "Idempotency-Key is required")
		return
	}
	if len(idempotencyKey) > 255 {
		writeAPIError(w, http.StatusBadRequest, "IDEMPOTENCY_KEY_INVALID", "Idempotency-Key is too long")
		return
	}

	if request.ProviderID == "" || request.ExternalTransactionID == "" ||
		request.RoundID == "" || request.GameID == "" {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "required fields are missing")
		return
	}

	playerID, err := uuid.Parse(request.PlayerID)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid playerId")
		return
	}
	walletID, err := uuid.Parse(request.WalletID)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid walletId")
		return
	}
	if !request.Money.IsValid() {
		writeAPIError(w, http.StatusBadRequest, "INVALID_MONEY", "invalid money")
		return
	}

	kind := domainwagering.Kind(request.Kind)
	switch kind {
	case domainwagering.KindBet, domainwagering.KindWin:
		if !request.Money.IsPositive() {
			writeAPIError(w, http.StatusBadRequest, "INVALID_MONEY", "amount must be positive")
			return
		}
	case domainwagering.KindLoss:
		if !request.Money.IsZero() {
			writeAPIError(w, http.StatusBadRequest, "LOSS_AMOUNT_NOT_ZERO", "loss amount must be zero")
			return
		}
	case domainwagering.KindOpening:
		writeAPIError(w, http.StatusBadRequest, "OPENING_NOT_ALLOWED", "opening is not allowed")
		return
	case domainwagering.KindRefund, domainwagering.KindRollback:
	default:
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid kind")
		return
	}

	correlationID := r.Header.Get("X-Correlation-Id")
	if correlationID == "" {
		correlationID = uuid.NewString()
	}

	result, err := h.service.ProcessTransaction(r.Context(), wageringapp.ProcessCommand{
		ProviderID:                     request.ProviderID,
		ExternalTransactionID:          request.ExternalTransactionID,
		IdempotencyKey:                 idempotencyKey,
		PlayerID:                       playerID,
		WalletID:                       walletID,
		RoundID:                        request.RoundID,
		GameID:                         request.GameID,
		Kind:                           kind,
		Amount:                         request.Money,
		ReferenceExternalTransactionID: request.ReferenceExternalTransactionID,
		CorrelationID:                  correlationID,
	})
	if err != nil {
		writeWageringServiceError(w, err)
		return
	}

	response := processTransactionResponse{
		TransactionID:    result.Transaction.ID().String(),
		Status:           string(result.Transaction.Status()),
		Balance:          result.Balance,
		IdempotentReplay: result.IdempotentReplay,
	}
	if failureCode := result.Transaction.FailureCode(); failureCode != nil {
		response.FailureCode = *failureCode
	}

	status := http.StatusOK
	if result.Transaction.Status() == domainwagering.StatusRejected ||
		result.Transaction.Status() == domainwagering.StatusFailed {
		status = http.StatusUnprocessableEntity
	}
	if result.Transaction.Status() == domainwagering.StatusPending ||
		result.Transaction.Status() == domainwagering.StatusPendingReference {
		status = http.StatusAccepted
	}

	writeJSON(w, status, response)
}

func (h *WageringHandler) Get(w http.ResponseWriter, r *http.Request) {
	transactionID, err := uuid.Parse(r.PathValue("transactionId"))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid transactionId")
		return
	}

	transaction, err := h.service.GetTransaction(r.Context(), transactionID)
	if err != nil {
		writeWageringServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toTransactionResponse(transaction))
}

func (h *WageringHandler) GetByProvider(w http.ResponseWriter, r *http.Request) {
	providerID := r.PathValue("providerId")
	externalTransactionID := r.PathValue("externalTransactionId")
	if providerID == "" || externalTransactionID == "" {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "providerId and externalTransactionId are required")
		return
	}

	transaction, err := h.service.GetProviderTransaction(r.Context(), providerID, externalTransactionID)
	if err != nil {
		writeWageringServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, toTransactionResponse(transaction))
}

func toTransactionResponse(transaction *domainwagering.Transaction) transactionResponse {
	response := transactionResponse{
		TransactionID:         transaction.ID().String(),
		ProviderID:            stringValue(transaction.ProviderID()),
		ExternalTransactionID: stringValue(transaction.ExternalTransactionID()),
		WalletID:              transaction.WalletID().String(),
		PlayerID:              transaction.PlayerID().String(),
		RoundID:               stringValue(transaction.RoundID()),
		GameID:                stringValue(transaction.GameID()),
		Kind:                  string(transaction.Kind()),
		Status:                string(transaction.Status()),
		FailureCode:           stringValue(transaction.FailureCode()),
		Money:                 transaction.Amount(),
		Balance:               transaction.ResultBalance(),
		ReferenceAttempts:     transaction.ReferenceAttempts(),
		NextReferenceAt:       transaction.NextReferenceAt(),
		CreatedAt:             transaction.CreatedAt(),
		ProcessedAt:           transaction.ProcessedAt(),
	}
	if reference := transaction.ReferenceExternalTransactionID(); reference != nil {
		response.ReferenceExternalTransactionID = *reference
	}
	if reference := transaction.ReferenceTransactionID(); reference != nil {
		response.ReferenceTransactionID = reference.String()
	}
	return response
}

func writeWageringServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domainwagering.ErrIdempotencyKeyConflict):
		writeAPIError(w, http.StatusConflict, "IDEMPOTENCY_KEY_CONFLICT", "idempotency key conflict")
	case errors.Is(err, domainwagering.ErrExternalTransactionConflict):
		writeAPIError(w, http.StatusConflict, "EXTERNAL_TRANSACTION_CONFLICT", "external transaction conflict")
	case errors.Is(err, domainwagering.ErrTransactionNotFound):
		writeAPIError(w, http.StatusNotFound, "TRANSACTION_NOT_FOUND", "transaction not found")
	case errors.Is(err, domainwallet.ErrWalletNotFound):
		writeAPIError(w, http.StatusNotFound, "WALLET_NOT_FOUND", "wallet not found")
	case errors.Is(err, wageringapp.ErrReferenceRequired):
		writeAPIError(w, http.StatusBadRequest, "REFERENCE_REQUIRED", "reference is required")
	case errors.Is(err, domainwagering.ErrDuplicateReversal):
		writeAPIError(w, http.StatusUnprocessableEntity, "DUPLICATE_REVERSAL", "duplicate reversal")
	case errors.Is(err, domainwagering.ErrUnsupportedKind):
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "kind not supported yet")
	case errors.Is(err, wageringapp.ErrInvalidCommand),
		errors.Is(err, domainwagering.ErrInvalidTransaction):
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid transaction")
	case errors.Is(err, domainwallet.ErrConcurrentUpdate):
		writeAPIError(w, http.StatusServiceUnavailable, "INFRA_UNAVAILABLE", "concurrent update")
	default:
		writeAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal error")
	}
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
