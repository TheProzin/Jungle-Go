package httpapi

import (
	"context"
	"net/http"
	"strings"

	infraauth "github.com/TheProzin/Jungle-Go/internal/infra/auth"
)

type contextKey string

const principalContextKey contextKey = "principal"

type Auth struct {
	authenticator *infraauth.Authenticator
}

func NewAuth(authenticator *infraauth.Authenticator) *Auth {
	return &Auth{authenticator: authenticator}
}

func (a *Auth) Internal(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal := a.authenticate(w, r)
		if principal == nil {
			return
		}
		if !principal.HasRole("internal") {
			writeAPIError(w, http.StatusForbidden, "FORBIDDEN_PROVIDER", "internal role required")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), principalContextKey, principal)))
	}
}

func (a *Auth) Provider(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal := a.authenticate(w, r)
		if principal == nil {
			return
		}
		if !principal.HasRole("provider") || principal.ProviderID == "" {
			writeAPIError(w, http.StatusForbidden, "FORBIDDEN_PROVIDER", "provider role required")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), principalContextKey, principal)))
	}
}

func (a *Auth) Authenticated(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal := a.authenticate(w, r)
		if principal == nil {
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), principalContextKey, principal)))
	}
}

func (a *Auth) authenticate(w http.ResponseWriter, r *http.Request) *infraauth.Principal {
	rawToken, ok := bearerToken(r.Header.Get("Authorization"))
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing bearer token")
		return nil
	}

	principal, err := a.authenticator.Validate(r.Context(), rawToken)
	if err != nil {
		writeAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid or expired token")
		return nil
	}
	return principal
}

func PrincipalFromContext(ctx context.Context) *infraauth.Principal {
	principal, _ := ctx.Value(principalContextKey).(*infraauth.Principal)
	return principal
}

func bearerToken(header string) (string, bool) {
	parts := strings.SplitN(strings.TrimSpace(header), " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}
