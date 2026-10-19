//go:build integration

package integration

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	wageringapp "github.com/TheProzin/Jungle-Go/internal/app/wagering"
	walletapp "github.com/TheProzin/Jungle-Go/internal/app/wallet"
	"github.com/TheProzin/Jungle-Go/internal/config"
	"github.com/TheProzin/Jungle-Go/internal/httpapi"
	authinfra "github.com/TheProzin/Jungle-Go/internal/infra/auth"
)

func newTestHTTPServer(
	t *testing.T,
	pool *pgxpool.Pool,
	walletService *walletapp.Service,
	wageringService *wageringapp.Service,
) *httptest.Server {
	t.Helper()

	cfg := keycloakTestConfig()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	slog.SetDefault(logger)
	health := httpapi.NewHealth(pool, nil, cfg, logger)
	authMiddleware := httpapi.NewAuth(authinfra.NewAuthenticator(cfg))

	return httptest.NewServer(httpapi.NewMux(
		health,
		httpapi.NewWalletHandler(walletService),
		httpapi.NewWageringHandler(wageringService),
		authMiddleware,
	))
}

func keycloakTestConfig() *config.Config {
	return &config.Config{
		KeycloakIssuer:          "http://localhost:8081/realms/jungle-go",
		KeycloakJWKSURL:         "http://localhost:8081/realms/jungle-go/protocol/openid-connect/certs",
		KeycloakAudience:        "wager-api",
		ReadinessTimeout:        2 * time.Second,
		SQSTransactionsQueueURL: "http://localhost:4566/000000000000/wager-transactions.fifo",
	}
}

func internalToken(t *testing.T) string {
	t.Helper()
	return tokenFor(t, "internal-service", "internal-secret")
}

func providerAToken(t *testing.T) string {
	t.Helper()
	return tokenFor(t, "provider-a", "provider-a-secret")
}

func providerBToken(t *testing.T) string {
	t.Helper()
	return tokenFor(t, "provider-b", "provider-b-secret")
}

func tokenFor(t *testing.T, clientID, secret string) string {
	t.Helper()

	response, err := http.PostForm(
		"http://localhost:8081/realms/jungle-go/protocol/openid-connect/token",
		url.Values{
			"grant_type":    {"client_credentials"},
			"client_id":     {clientID},
			"client_secret": {secret},
		},
	)
	if err != nil {
		t.Fatalf("obtain token for %s: %v", clientID, err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read token response: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("token status for %s = %d, body = %s", clientID, response.StatusCode, body)
	}

	var payload struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode token: %v", err)
	}
	if payload.AccessToken == "" {
		t.Fatalf("empty access token for %s", clientID)
	}
	return payload.AccessToken
}

func authHeaders(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token}
}

func mergeHeaders(base, extra map[string]string) map[string]string {
	merged := make(map[string]string, len(base)+len(extra))
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range extra {
		merged[key] = value
	}
	return merged
}
