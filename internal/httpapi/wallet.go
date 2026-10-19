package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	walletapp "github.com/TheProzin/Jungle-Go/internal/app/wallet"
	"github.com/TheProzin/Jungle-Go/internal/domain/money"
	domainwallet "github.com/TheProzin/Jungle-Go/internal/domain/wallet"
)

type WalletHandler struct {
	service *walletapp.Service
}

type createWalletRequest struct {
	PlayerID       string      `json:"playerId"`
	InitialBalance money.Money `json:"initialBalance"`
}

type walletResponse struct {
	ID       string      `json:"id"`
	PlayerID string      `json:"playerId"`
	Balance  money.Money `json:"balance"`
	Version  int64       `json:"version"`
}

type ledgerEntryResponse struct {
	ID            string      `json:"id"`
	TransactionID string      `json:"transactionId"`
	Direction     string      `json:"direction"`
	Money         money.Money `json:"money"`
	BalanceBefore money.Money `json:"balanceBefore"`
	BalanceAfter  money.Money `json:"balanceAfter"`
	CreatedAt     time.Time   `json:"createdAt"`
}

type ledgerPageResponse struct {
	Items      []ledgerEntryResponse `json:"items"`
	NextCursor string                `json:"nextCursor,omitempty"`
}

type reconciliationResponse struct {
	WalletID          string      `json:"walletId"`
	StoredBalance     money.Money `json:"storedBalance"`
	CalculatedBalance money.Money `json:"calculatedBalance"`
	Difference        money.Money `json:"difference"`
	Consistent        bool        `json:"consistent"`
	CheckedEntries    int         `json:"checkedEntries"`
}

func NewWalletHandler(service *walletapp.Service) *WalletHandler {
	return &WalletHandler{service: service}
}

func (h *WalletHandler) Create(w http.ResponseWriter, r *http.Request) {
	var request createWalletRequest
	if err := decodeJSON(w, r, &request); err != nil {
		if errors.Is(err, money.ErrInvalidAmount) || errors.Is(err, money.ErrInvalidCurrency) {
			writeAPIError(w, http.StatusBadRequest, "INVALID_MONEY", "invalid initialBalance")
			return
		}
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	playerID, err := uuid.Parse(request.PlayerID)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid playerId")
		return
	}
	if !request.InitialBalance.IsValid() {
		writeAPIError(w, http.StatusBadRequest, "INVALID_MONEY", "invalid initialBalance")
		return
	}

	correlationID := CorrelationIDFromContext(r.Context())
	if correlationID == "" {
		correlationID = r.Header.Get("X-Correlation-Id")
	}
	if correlationID == "" {
		correlationID = uuid.NewString()
	}

	created, err := h.service.CreateWallet(r.Context(), walletapp.CreateWalletCommand{
		PlayerID:       playerID,
		InitialBalance: request.InitialBalance,
		CorrelationID:  correlationID,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}

	slog.Default().Info(
		"wallet created",
		"walletId", created.ID().String(),
		"playerId", created.PlayerID().String(),
		"correlationId", correlationID,
	)
	writeJSON(w, http.StatusCreated, walletResponse{
		ID:       created.ID().String(),
		PlayerID: created.PlayerID().String(),
		Balance:  created.Balance(),
		Version:  created.Version(),
	})
}

func (h *WalletHandler) Get(w http.ResponseWriter, r *http.Request) {
	walletID, ok := parseWalletID(w, r)
	if !ok {
		return
	}

	found, err := h.service.GetWallet(r.Context(), walletID)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, walletResponse{
		ID:       found.ID().String(),
		PlayerID: found.PlayerID().String(),
		Balance:  found.Balance(),
		Version:  found.Version(),
	})
}

func (h *WalletHandler) Ledger(w http.ResponseWriter, r *http.Request) {
	walletID, ok := parseWalletID(w, r)
	if !ok {
		return
	}

	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid limit")
			return
		}
		limit = parsed
	}

	page, err := h.service.GetLedger(r.Context(), walletID, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	items := make([]ledgerEntryResponse, 0, len(page.Entries))
	for _, entry := range page.Entries {
		items = append(items, ledgerEntryResponse{
			ID:            entry.ID().String(),
			TransactionID: entry.TransactionID().String(),
			Direction:     string(entry.Direction()),
			Money:         entry.Amount(),
			BalanceBefore: entry.BalanceBefore(),
			BalanceAfter:  entry.BalanceAfter(),
			CreatedAt:     entry.CreatedAt(),
		})
	}

	writeJSON(w, http.StatusOK, ledgerPageResponse{
		Items:      items,
		NextCursor: page.NextCursor,
	})
}

func (h *WalletHandler) Reconcile(w http.ResponseWriter, r *http.Request) {
	walletID, ok := parseWalletID(w, r)
	if !ok {
		return
	}

	result, err := h.service.Reconcile(r.Context(), walletID)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, reconciliationResponse{
		WalletID:          result.WalletID.String(),
		StoredBalance:     result.StoredBalance,
		CalculatedBalance: result.CalculatedBalance,
		Difference:        result.Difference,
		Consistent:        result.Consistent,
		CheckedEntries:    result.CheckedEntries,
	})
}

func parseWalletID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	walletID, err := uuid.Parse(r.PathValue("walletId"))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid walletId")
		return uuid.Nil, false
	}
	return walletID, true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("trailing data")
	}
	return nil
}

func writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domainwallet.ErrWalletAlreadyExists):
		writeAPIError(w, http.StatusConflict, "WALLET_ALREADY_EXISTS", "wallet already exists")
	case errors.Is(err, domainwallet.ErrWalletNotFound):
		writeAPIError(w, http.StatusNotFound, "WALLET_NOT_FOUND", "wallet not found")
	case errors.Is(err, domainwallet.ErrNegativeInitialBalance),
		errors.Is(err, domainwallet.ErrInvalidWallet):
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid wallet")
	case errors.Is(err, money.ErrInvalidAmount),
		errors.Is(err, money.ErrInvalidCurrency):
		writeAPIError(w, http.StatusBadRequest, "INVALID_MONEY", "invalid money")
	case errors.Is(err, walletapp.ErrInvalidCursor):
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid cursor")
	default:
		writeAPIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal error")
	}
}

func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"code":      code,
			"message":   message,
			"retryable": status == http.StatusServiceUnavailable,
		},
	})
}
