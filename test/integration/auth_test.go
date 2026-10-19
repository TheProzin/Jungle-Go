//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	wageringapp "github.com/TheProzin/Jungle-Go/internal/app/wagering"
	walletapp "github.com/TheProzin/Jungle-Go/internal/app/wallet"
	"github.com/TheProzin/Jungle-Go/internal/infra/postgres"
)

func TestAuthenticationAndAuthorization(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	unitOfWork := postgres.NewUnitOfWork(pool)
	walletService := walletapp.NewService(unitOfWork)
	wageringService := wageringapp.NewService(unitOfWork)
	server := newTestHTTPServer(t, pool, walletService, wageringService)
	defer server.Close()
	client := server.Client()

	internal := authHeaders(internalToken(t))
	providerA := authHeaders(providerAToken(t))
	providerB := authHeaders(providerBToken(t))

	healthResponse, _ := doJSON(t, client, http.MethodGet, server.URL+"/health/live", nil, nil)
	if healthResponse.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d, want 200", healthResponse.StatusCode)
	}

	playerID := uuid.New()
	walletBody := map[string]any{
		"playerId":       playerID.String(),
		"initialBalance": map[string]string{"amount": "100.00", "currency": "BRL"},
	}

	noTokenResponse, _ := doJSON(t, client, http.MethodPost, server.URL+"/wallets", nil, walletBody)
	if noTokenResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d, want 401", noTokenResponse.StatusCode)
	}

	invalidTokenResponse, _ := doJSON(t, client, http.MethodPost, server.URL+"/wallets", authHeaders("invalid-token"), walletBody)
	if invalidTokenResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("invalid token status = %d, want 401", invalidTokenResponse.StatusCode)
	}

	providerWalletResponse, _ := doJSON(t, client, http.MethodPost, server.URL+"/wallets", providerA, walletBody)
	if providerWalletResponse.StatusCode != http.StatusForbidden {
		t.Fatalf("provider wallet status = %d, want 403", providerWalletResponse.StatusCode)
	}

	createResponse, createBody := doJSON(t, client, http.MethodPost, server.URL+"/wallets", internal, walletBody)
	if createResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create wallet status = %d, body = %s", createResponse.StatusCode, createBody)
	}
	var wallet struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(createBody, &wallet); err != nil {
		t.Fatalf("decode wallet: %v", err)
	}

	providerAExternalID := "auth-provider-a-" + uuid.NewString()
	providerAKey := "auth-provider-a-key-" + uuid.NewString()
	providerABody := processRequestBody("provider-a", providerAExternalID, playerID.String(), wallet.ID, "round-1", "game-1", "BET", "10.00", "")

	internalProcessResponse, _ := doJSON(t, client, http.MethodPost, server.URL+"/wagering/transactions", mergeHeaders(internal, map[string]string{"Idempotency-Key": providerAKey}), providerABody)
	if internalProcessResponse.StatusCode != http.StatusForbidden {
		t.Fatalf("internal process status = %d, want 403", internalProcessResponse.StatusCode)
	}

	providerAProcessResponse, providerAProcessBody := doJSON(t, client, http.MethodPost, server.URL+"/wagering/transactions", mergeHeaders(providerA, map[string]string{"Idempotency-Key": providerAKey}), providerABody)
	if providerAProcessResponse.StatusCode != http.StatusOK {
		t.Fatalf("provider-a process status = %d, body = %s", providerAProcessResponse.StatusCode, providerAProcessBody)
	}
	var providerATransaction struct {
		TransactionID string `json:"transactionId"`
	}
	if err := json.Unmarshal(providerAProcessBody, &providerATransaction); err != nil {
		t.Fatalf("decode provider-a transaction: %v", err)
	}

	forbiddenBody := processRequestBody("provider-b", "auth-forbidden-"+uuid.NewString(), playerID.String(), wallet.ID, "round-1", "game-1", "BET", "10.00", "")
	forbiddenResponse, _ := doJSON(t, client, http.MethodPost, server.URL+"/wagering/transactions", mergeHeaders(providerA, map[string]string{"Idempotency-Key": "auth-forbidden-key-" + uuid.NewString()}), forbiddenBody)
	if forbiddenResponse.StatusCode != http.StatusForbidden {
		t.Fatalf("provider mismatch status = %d, want 403", forbiddenResponse.StatusCode)
	}

	providerBExternalID := "auth-provider-b-" + uuid.NewString()
	providerBKey := "auth-provider-b-key-" + uuid.NewString()
	providerBBody := processRequestBody("provider-b", providerBExternalID, playerID.String(), wallet.ID, "round-1", "game-1", "BET", "10.00", "")
	providerBResponse, providerBBodyResponse := doJSON(t, client, http.MethodPost, server.URL+"/wagering/transactions", mergeHeaders(providerB, map[string]string{"Idempotency-Key": providerBKey}), providerBBody)
	if providerBResponse.StatusCode != http.StatusOK {
		t.Fatalf("provider-b process status = %d, body = %s", providerBResponse.StatusCode, providerBBodyResponse)
	}
	var providerBTransaction struct {
		TransactionID string `json:"transactionId"`
	}
	if err := json.Unmarshal(providerBBodyResponse, &providerBTransaction); err != nil {
		t.Fatalf("decode provider-b transaction: %v", err)
	}

	getResponse, _ := doJSON(t, client, http.MethodGet, server.URL+"/wagering/transactions/"+providerATransaction.TransactionID, providerA, nil)
	if getResponse.StatusCode != http.StatusOK {
		t.Fatalf("provider-a own transaction status = %d, want 200", getResponse.StatusCode)
	}

	crossProviderResponse, _ := doJSON(t, client, http.MethodGet, server.URL+"/wagering/transactions/"+providerATransaction.TransactionID, providerB, nil)
	if crossProviderResponse.StatusCode != http.StatusForbidden {
		t.Fatalf("cross provider transaction status = %d, want 403", crossProviderResponse.StatusCode)
	}

	providerBTransactionResponse, _ := doJSON(t, client, http.MethodGet, server.URL+"/wagering/transactions/"+providerBTransaction.TransactionID, providerA, nil)
	if providerBTransactionResponse.StatusCode != http.StatusForbidden {
		t.Fatalf("provider-a reading provider-b status = %d, want 403", providerBTransactionResponse.StatusCode)
	}

	internalTransactionResponse, _ := doJSON(t, client, http.MethodGet, server.URL+"/wagering/transactions/"+providerBTransaction.TransactionID, internal, nil)
	if internalTransactionResponse.StatusCode != http.StatusOK {
		t.Fatalf("internal transaction read status = %d, want 200", internalTransactionResponse.StatusCode)
	}

	providerAPathResponse, _ := doJSON(t, client, http.MethodGet, server.URL+"/providers/provider-a/wagering/transactions/"+providerAExternalID, providerA, nil)
	if providerAPathResponse.StatusCode != http.StatusOK {
		t.Fatalf("provider-a own path status = %d, want 200", providerAPathResponse.StatusCode)
	}

	providerBPathResponse, _ := doJSON(t, client, http.MethodGet, server.URL+"/providers/provider-b/wagering/transactions/"+providerBExternalID, providerA, nil)
	if providerBPathResponse.StatusCode != http.StatusForbidden {
		t.Fatalf("provider-a reading provider-b path status = %d, want 403", providerBPathResponse.StatusCode)
	}

	providerWalletGetResponse, _ := doJSON(t, client, http.MethodGet, server.URL+"/wallets/"+wallet.ID, providerA, nil)
	if providerWalletGetResponse.StatusCode != http.StatusForbidden {
		t.Fatalf("provider wallet read status = %d, want 403", providerWalletGetResponse.StatusCode)
	}

	reconciliationResponse, _ := doJSON(t, client, http.MethodPost, server.URL+"/wallets/"+wallet.ID+"/reconciliation", providerA, nil)
	if reconciliationResponse.StatusCode != http.StatusForbidden {
		t.Fatalf("provider reconciliation status = %d, want 403", reconciliationResponse.StatusCode)
	}

	assertCount(t, ctx, pool, `SELECT COUNT(*) FROM wager_transactions WHERE provider_id = 'provider-b' AND external_transaction_id = '`+providerBExternalID+`'`, 1)
	assertCount(t, ctx, pool, `SELECT COUNT(*) FROM wager_transactions WHERE external_transaction_id LIKE 'auth-forbidden-%'`, 0)
}

func TestMetricsEndpoint(t *testing.T) {
	pool := newTestPool(t)
	unitOfWork := postgres.NewUnitOfWork(pool)
	walletService := walletapp.NewService(unitOfWork)
	wageringService := wageringapp.NewService(unitOfWork)
	server := newTestHTTPServer(t, pool, walletService, wageringService)
	defer server.Close()
	client := server.Client()

	internal := authHeaders(internalToken(t))
	provider := authHeaders(providerAToken(t))

	createResponse, createBody := doJSON(t, client, http.MethodPost, server.URL+"/wallets", internal, map[string]any{
		"playerId":       uuid.New().String(),
		"initialBalance": map[string]string{"amount": "10.00", "currency": "BRL"},
	})
	if createResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create wallet status = %d, body = %s", createResponse.StatusCode, createBody)
	}

	noTokenResponse, _ := doJSON(t, client, http.MethodGet, server.URL+"/metrics", nil, nil)
	if noTokenResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("metrics without token status = %d, want 401", noTokenResponse.StatusCode)
	}

	providerResponse, _ := doJSON(t, client, http.MethodGet, server.URL+"/metrics", provider, nil)
	if providerResponse.StatusCode != http.StatusForbidden {
		t.Fatalf("metrics with provider token status = %d, want 403", providerResponse.StatusCode)
	}

	response, body := doJSON(t, client, http.MethodGet, server.URL+"/metrics", internal, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("metrics status = %d, body = %s", response.StatusCode, body)
	}
	text := string(body)
	for _, metric := range []string{
		"wager_transactions_total",
		"wager_outbox_pending",
		"wager_request_duration_seconds",
		"wager_reconciliation_divergences_total",
	} {
		if !strings.Contains(text, metric) {
			t.Fatalf("metrics body does not contain %q", metric)
		}
	}
}

func TestExpiredTokenIsRejected(t *testing.T) {
	restore := setRealmAccessTokenLifespan(t, 1)
	defer restore()

	pool := newTestPool(t)
	unitOfWork := postgres.NewUnitOfWork(pool)
	walletService := walletapp.NewService(unitOfWork)
	wageringService := wageringapp.NewService(unitOfWork)
	server := newTestHTTPServer(t, pool, walletService, wageringService)
	defer server.Close()
	client := server.Client()

	token := internalToken(t)
	time.Sleep(2 * time.Second)

	response, body := doJSON(t, client, http.MethodPost, server.URL+"/wallets", authHeaders(token), map[string]any{
		"playerId":       uuid.New().String(),
		"initialBalance": map[string]string{"amount": "10.00", "currency": "BRL"},
	})
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expired token status = %d, body = %s", response.StatusCode, body)
	}
	assertCount(t, context.Background(), pool, `SELECT COUNT(*) FROM wallets`, 0)
}

func setRealmAccessTokenLifespan(t *testing.T, seconds int) func() {
	t.Helper()

	adminToken := keycloakAdminToken(t)
	realmURL := "http://localhost:8081/admin/realms/jungle-go"

	request, err := http.NewRequest(http.MethodGet, realmURL, nil)
	if err != nil {
		t.Fatalf("create realm request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+adminToken)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("get realm: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("get realm status = %d", response.StatusCode)
	}

	var realm map[string]any
	if err := json.NewDecoder(response.Body).Decode(&realm); err != nil {
		t.Fatalf("decode realm: %v", err)
	}
	previousLifespan, ok := realm["accessTokenLifespan"].(float64)
	if !ok {
		previousLifespan = 300
	}
	realm["accessTokenLifespan"] = float64(seconds)
	putRealm(t, adminToken, realmURL, realm)

	return func() {
		realm["accessTokenLifespan"] = previousLifespan
		putRealm(t, adminToken, realmURL, realm)
	}
}

func keycloakAdminToken(t *testing.T) string {
	t.Helper()

	response, err := http.PostForm(
		"http://localhost:8081/realms/master/protocol/openid-connect/token",
		url.Values{
			"grant_type": {"password"},
			"client_id":  {"admin-cli"},
			"username":   {"admin"},
			"password":   {"admin"},
		},
	)
	if err != nil {
		t.Fatalf("obtain admin token: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("admin token status = %d", response.StatusCode)
	}

	var payload struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode admin token: %v", err)
	}
	return payload.AccessToken
}

func putRealm(t *testing.T, adminToken, realmURL string, realm map[string]any) {
	t.Helper()

	body, err := json.Marshal(realm)
	if err != nil {
		t.Fatalf("marshal realm: %v", err)
	}
	request, err := http.NewRequest(http.MethodPut, realmURL, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create realm update request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+adminToken)
	request.Header.Set("Content-Type", "application/json")

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("update realm: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("update realm status = %d", response.StatusCode)
	}
}
