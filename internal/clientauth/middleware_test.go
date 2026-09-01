package clientauth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/neverknowerdev/paylessforai/internal/db/repositories"
)

type fakeAuthenticator struct {
	key repositories.ClientKey
	ok  bool
	err error
}

func (f fakeAuthenticator) Authenticate(context.Context, string) (repositories.ClientKey, bool, error) {
	return f.key, f.ok, f.err
}

func TestMiddlewareSupportsBearerAndAPIKeyAndStoresOnlyID(t *testing.T) {
	for _, header := range []string{"Authorization", "x-api-key"} {
		t.Run(header, func(t *testing.T) {
			auth := Middleware(fakeAuthenticator{key: repositories.ClientKey{ID: "key-1"}, ok: true})
			var got string
			handler := auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got, _ = KeyID(r.Context())
				w.WriteHeader(http.StatusNoContent)
			}))
			req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
			if header == "Authorization" {
				req.Header.Set(header, "Bearer secret")
			} else {
				req.Header.Set(header, "secret")
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != http.StatusNoContent || got != "key-1" {
				t.Fatalf("unexpected auth result: %d %q", response.Code, got)
			}
		})
	}
}

func TestMiddlewareAuthorizationHeaderHasPrecedence(t *testing.T) {
	var received string
	auth := Middleware(authenticatorFunc(func(_ context.Context, secret string) (repositories.ClientKey, bool, error) {
		received = secret
		return repositories.ClientKey{ID: "key"}, true, nil
	}))
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer bearer-secret")
	req.Header.Set("x-api-key", "header-secret")
	response := httptest.NewRecorder()
	auth(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(response, req)
	if received != "bearer-secret" {
		t.Fatalf("got %q, want bearer-secret", received)
	}
}

func TestMiddlewareRejectsMissingMalformedInvalidAndLookupError(t *testing.T) {
	tests := []struct {
		name   string
		header string
		auth   Authenticator
		code   int
	}{
		{name: "missing", code: http.StatusUnauthorized, auth: fakeAuthenticator{ok: true}},
		{name: "malformed", header: "Basic secret", code: http.StatusUnauthorized, auth: fakeAuthenticator{ok: true}},
		{name: "invalid", header: "Bearer secret", code: http.StatusUnauthorized, auth: fakeAuthenticator{}},
		{name: "lookup error", header: "Bearer secret", code: http.StatusInternalServerError, auth: fakeAuthenticator{err: errors.New("db")}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
			if test.header != "" {
				req.Header.Set("Authorization", test.header)
			}
			response := httptest.NewRecorder()
			Middleware(test.auth)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(response, req)
			if response.Code != test.code {
				t.Fatalf("got %d, want %d: %s", response.Code, test.code, response.Body.String())
			}
		})
	}
}

type authenticatorFunc func(context.Context, string) (repositories.ClientKey, bool, error)

func (f authenticatorFunc) Authenticate(ctx context.Context, secret string) (repositories.ClientKey, bool, error) {
	return f(ctx, secret)
}
