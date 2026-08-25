// Package localproxy implements the client-side adapter used by IDEs.
// It intentionally contains no provider or routing logic: every API request
// is forwarded to the hosted server, which remains the source of truth.
package localproxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

type Config struct {
	ListenAddr        string
	RemoteURL         string
	ServerAPIKey      string
	ReadHeaderTimeout time.Duration
	IdleTimeout       time.Duration
}

func DefaultConfig() Config {
	return Config{
		ListenAddr:        "127.0.0.1:9473",
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
}

func (c Config) Validate() error {
	if c.ListenAddr == "" {
		return errors.New("listen address is required")
	}
	if _, _, err := net.SplitHostPort(c.ListenAddr); err != nil {
		return fmt.Errorf("invalid listen address: %w", err)
	}
	if c.RemoteURL == "" {
		return errors.New("server URL is required")
	}
	u, err := url.Parse(c.RemoteURL)
	if err != nil || u.Scheme == "" || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("invalid server URL %q", c.RemoteURL)
	}
	if c.ReadHeaderTimeout <= 0 || c.IdleTimeout <= 0 {
		return errors.New("timeouts must be positive")
	}
	return nil
}

// NewHandler creates the local proxy handler. The server API key is used only
// when the IDE did not supply an Authorization or x-api-key header, allowing
// both transparent key forwarding and one-key local configuration.
func NewHandler(cfg Config) (http.Handler, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	target, err := url.Parse(cfg.RemoteURL)
	if err != nil {
		return nil, err
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		if req.Header.Get("Authorization") == "" && req.Header.Get("X-API-Key") == "" && cfg.ServerAPIKey != "" {
			req.Header.Set("Authorization", "Bearer "+cfg.ServerAPIKey)
		}
		// The local listener is not the authority for the hosted service.
		req.Header.Del("Host")
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		writeError(w, http.StatusBadGateway, "remote_unavailable", err.Error())
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","role":"app","remote":true}`))
	})
	mux.Handle("/", withRequestID(proxy))
	return withRequestID(mux), nil
}

func NewServer(cfg Config) (http.Server, error) {
	h, err := NewHandler(cfg)
	if err != nil {
		return http.Server{}, err
	}
	return http.Server{Addr: cfg.ListenAddr, Handler: h, ReadHeaderTimeout: cfg.ReadHeaderTimeout, IdleTimeout: cfg.IdleTimeout}, nil
}

func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if w.Header().Get("X-PayLess-Request-ID") == "" {
			w.Header().Set("X-PayLess-Request-ID", fmt.Sprintf("local-%d", time.Now().UnixNano()))
		}
		next.ServeHTTP(w, r)
	})
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"type": code, "message": strings.TrimSpace(message)}})
}
