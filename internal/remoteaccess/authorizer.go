package remoteaccess

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"
)

type IdentityResolver interface {
	WhoIs(context.Context, string) (string, error)
}

func OwnerAuthorizer(resolver IdentityResolver, owner func() string) Authorizer {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			caller, err := resolver.WhoIs(r.Context(), r.RemoteAddr)
			if err != nil || caller == "" || owner() == "" || caller != owner() {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ProtectManagement implements a strict double-submit CSRF token for the
// private browser control plane. The token is not HttpOnly because the UI
// must copy it into the request header; SameSite and exact-origin checks still
// prevent cross-site submissions.
func ProtectManagement(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			if r.Method == http.MethodGet && !strings.Contains(r.Header.Get("Cookie"), "plai_csrf=") {
				setCSRF(w)
			}
			next.ServeHTTP(w, r)
			return
		}
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		expected := "http://" + r.Host
		if r.TLS != nil {
			expected = "https://" + r.Host
		}
		cookie, err := r.Cookie("plai_csrf")
		token := strings.TrimSpace(r.Header.Get("X-CSRF-Token"))
		if origin != expected || err != nil || token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(cookie.Value)) != 1 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func setCSRF(w http.ResponseWriter) {
	buffer := make([]byte, 24)
	if _, err := rand.Read(buffer); err != nil {
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "plai_csrf", Value: base64.RawURLEncoding.EncodeToString(buffer), Path: "/", Secure: true, SameSite: http.SameSiteStrictMode})
}
