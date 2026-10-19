package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/TheProzin/Jungle-Go/internal/config"
)

var ErrInvalidToken = errors.New("invalid token")

type Principal struct {
	Subject    string
	ProviderID string
	roles      map[string]bool
}

func (p *Principal) HasRole(role string) bool {
	return p.roles[role]
}

type Authenticator struct {
	verifier *oidc.IDTokenVerifier
}

func NewAuthenticator(cfg *config.Config) *Authenticator {
	keySet := oidc.NewRemoteKeySet(context.Background(), cfg.KeycloakJWKSURL)
	verifier := oidc.NewVerifier(cfg.KeycloakIssuer, keySet, &oidc.Config{
		ClientID: cfg.KeycloakAudience,
	})
	return &Authenticator{verifier: verifier}
}

func (a *Authenticator) Validate(ctx context.Context, rawToken string) (*Principal, error) {
	token, err := a.verifier.Verify(ctx, rawToken)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}

	var claims struct {
		Roles       json.RawMessage `json:"roles"`
		ProviderID  string          `json:"provider_id"`
		RealmAccess struct {
			Roles []string `json:"roles"`
		} `json:"realm_access"`
	}
	if err := token.Claims(&claims); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}

	roles := make(map[string]bool)
	for _, role := range claims.RealmAccess.Roles {
		roles[role] = true
	}
	parseRoles(claims.Roles, roles)

	return &Principal{
		Subject:    token.Subject,
		ProviderID: claims.ProviderID,
		roles:      roles,
	}, nil
}

func parseRoles(raw json.RawMessage, roles map[string]bool) {
	if len(raw) == 0 {
		return
	}

	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		if single != "" {
			roles[single] = true
		}
		return
	}

	var many []string
	if err := json.Unmarshal(raw, &many); err == nil {
		for _, role := range many {
			roles[role] = true
		}
	}
}
