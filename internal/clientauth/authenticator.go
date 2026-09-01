// Package clientauth authenticates PayLessForAI client keys at the HTTP
// boundary. It deliberately stores only the key ID in request context.
package clientauth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/neverknowerdev/paylessforai/internal/db/repositories"
)

type Authenticator interface {
	Authenticate(context.Context, string) (repositories.ClientKey, bool, error)
}

type contextKey struct{}

// KeyID returns the authenticated PayLessForAI client-key ID, if middleware
// has already authenticated this request.
func KeyID(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(contextKey{}).(string)
	return id, ok && id != ""
}

// Middleware authenticates only requests that reach it. This lets a strict
// allow-list handler return 404 for non-API paths without disclosing whether a
// caller has a valid key.
func Middleware(auth Authenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			secret, present, validSyntax := credential(r)
			if !present || !validSyntax || auth == nil {
				writeError(w, http.StatusUnauthorized, "invalid_api_key", "a PayLessForAI client API key is required")
				return
			}
			key, ok, err := auth.Authenticate(r.Context(), secret)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "key_lookup_failed", "client key lookup failed")
				return
			}
			if !ok {
				writeError(w, http.StatusUnauthorized, "invalid_api_key", "the client API key is invalid or revoked")
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), contextKey{}, key.ID)))
		})
	}
}

// AllowAll is intended for tests of handlers that are not testing key
// authentication itself.
func AllowAll(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), contextKey{}, "test-client")))
	})
}

func credential(r *http.Request) (string, bool, bool) {
	if value := strings.TrimSpace(r.Header.Get("Authorization")); value != "" {
		parts := strings.Fields(value)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
			return "", true, false
		}
		return parts[1], true, true
	}
	if value := strings.TrimSpace(r.Header.Get("x-api-key")); value != "" {
		return value, true, true
	}
	return "", false, false
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"type": code, "message": message}})
}
