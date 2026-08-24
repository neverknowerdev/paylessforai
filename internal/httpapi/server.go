package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/neverknowerdev/paylessforai/internal/catalog"
	"github.com/neverknowerdev/paylessforai/internal/ids"
	"github.com/neverknowerdev/paylessforai/internal/matcher"
	proxyservice "github.com/neverknowerdev/paylessforai/internal/proxy"
	"github.com/neverknowerdev/paylessforai/internal/secrets"
	"github.com/neverknowerdev/paylessforai/internal/store"
	"github.com/neverknowerdev/paylessforai/internal/web"
)

type Server struct {
	httpServer *http.Server
	ready      bool
}

type CredentialDeps struct {
	Box           *secrets.Box
	ProviderBases map[string]string
	Reload        func() error
}

func New(addr string, readHeaderTimeout, idleTimeout time.Duration, db *store.Store) (*Server, error) {
	return NewWithDeps(addr, readHeaderTimeout, idleTimeout, db, nil, nil)
}

func NewWithDeps(addr string, readHeaderTimeout, idleTimeout time.Duration, db *store.Store, catalogManager *catalog.Manager, proxyHandler *proxyservice.Proxy, credentialConfig ...CredentialDeps) (*Server, error) {
	credentials := CredentialDeps{}
	if len(credentialConfig) > 0 {
		credentials = credentialConfig[0]
	}
	ui, err := web.Handler()
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if db == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ready": false})
			return
		}
		if err := db.DB().Ping(); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ready": false, "error": "database unavailable"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ready": true})
	})
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, _ *http.Request) {
		ready := db != nil && db.DB().Ping() == nil
		status := map[string]any{"ready": ready}
		if catalogManager != nil {
			snapshot := catalogManager.Snapshot()
			status["catalog_updated_at"] = snapshot.UpdatedAt
			status["model_count"] = len(snapshot.Models)
			status["route_count"] = len(snapshot.Routes)
		}
		writeJSON(w, http.StatusOK, status)
	})
	mux.HandleFunc("/api/requests", func(w http.ResponseWriter, r *http.Request) {
		if db == nil || r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "request statistics only accepts GET")
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		items, err := db.ListRequestStats(r.Context(), limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "request_stats_failed", "could not list request statistics")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": items})
	})
	mux.HandleFunc("/api/stats/summary", func(w http.ResponseWriter, r *http.Request) {
		if db == nil || r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "statistics summary only accepts GET")
			return
		}
		summary, err := db.RequestStatsSummary(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "stats_summary_failed", "could not load statistics summary")
			return
		}
		writeJSON(w, http.StatusOK, summary)
	})
	mux.HandleFunc("/api/models", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "model catalog only accepts GET")
			return
		}
		if catalogManager == nil {
			writeJSON(w, http.StatusOK, map[string]any{"data": []any{}})
			return
		}
		routes := catalogManager.Snapshot().Routes
		data := make([]map[string]any, 0, len(routes))
		seen := make(map[string]struct{}, len(routes))
		for _, route := range routes {
			key := route.Provider + "\x00" + route.LogicalModel + "\x00" + route.UpstreamModel
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			data = append(data, map[string]any{
				"id": route.ID, "provider": route.Provider, "model": route.LogicalModel, "upstream_model": route.UpstreamModel,
				"free": route.Free, "price_available": route.PriceAvailable, "health": route.Health,
				"context_length": route.Capabilities.MaxContext, "max_output_tokens": route.Capabilities.MaxOutput,
				"supported_parameters": route.Capabilities.Parameters,
				"pricing": map[string]any{
					"input": route.Price.InputPicoUSDPerToken, "output": route.Price.OutputPicoUSDPerToken,
					"cached_read": route.Price.CachedReadPicoUSDPerToken, "cache_write": route.Price.CacheWritePicoUSDPerToken,
					"reasoning": route.Price.ReasoningPicoUSDPerToken, "fixed": route.Price.FixedPicoUSD,
				},
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": data})
	})
	mux.HandleFunc("/api/client-keys", func(w http.ResponseWriter, r *http.Request) {
		if db == nil {
			writeError(w, http.StatusServiceUnavailable, "database_unavailable", "database is unavailable")
			return
		}
		switch r.Method {
		case http.MethodGet:
			keys, err := db.ListClientKeys(r.Context())
			if err != nil {
				writeError(w, http.StatusInternalServerError, "key_list_failed", "could not list client keys")
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"data": keys})
		case http.MethodPost:
			var input struct {
				Label string `json:"label"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input); err != nil && err != io.EOF {
				writeError(w, http.StatusBadRequest, "invalid_request", "invalid key request")
				return
			}
			key, secret, err := db.CreateClientKey(r.Context(), input.Label)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "key_create_failed", "could not create client key")
				return
			}
			writeJSON(w, http.StatusCreated, map[string]any{"key": key, "secret": secret})
		default:
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "client keys endpoint only accepts GET and POST")
		}
	})
	mux.HandleFunc("/api/client-keys/", func(w http.ResponseWriter, r *http.Request) {
		if db == nil || r.Method != http.MethodDelete {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "client key deletion only accepts DELETE")
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/api/client-keys/")
		if id == "" {
			writeError(w, http.StatusBadRequest, "invalid_request", "client key ID is required")
			return
		}
		if err := db.RevokeClientKey(r.Context(), id); err != nil {
			writeError(w, http.StatusInternalServerError, "key_revoke_failed", "could not revoke client key")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"revoked": true})
	})
	mux.HandleFunc("/api/providers/credentials", func(w http.ResponseWriter, r *http.Request) {
		if db == nil {
			writeError(w, http.StatusServiceUnavailable, "database_unavailable", "database is unavailable")
			return
		}
		switch r.Method {
		case http.MethodGet:
			items, err := db.ListProviderCredentials(r.Context())
			if err != nil {
				writeError(w, http.StatusInternalServerError, "credential_list_failed", "could not list provider credentials")
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"data": items})
		case http.MethodPost:
			if credentials.Box == nil {
				writeError(w, http.StatusServiceUnavailable, "credential_store_unavailable", "encrypted credential storage is unavailable")
				return
			}
			var input struct {
				Provider string `json:"provider"`
				Label    string `json:"label"`
				APIKey   string `json:"api_key"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input); err != nil || strings.TrimSpace(input.Provider) == "" || strings.TrimSpace(input.APIKey) == "" {
				writeError(w, http.StatusBadRequest, "invalid_request", "provider, label, and api_key are required")
				return
			}
			input.Provider = strings.ToLower(strings.TrimSpace(input.Provider))
			if _, ok := credentials.ProviderBases[input.Provider]; !ok {
				writeError(w, http.StatusBadRequest, "unsupported_provider", "provider is not configured")
				return
			}
			ciphertext, nonce, err := credentials.Box.Seal(input.APIKey)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "credential_encrypt_failed", "could not encrypt provider credential")
				return
			}
			item := store.ProviderCredential{ID: ids.New(), Provider: input.Provider, Label: input.Label, Ciphertext: ciphertext, Nonce: nonce, Enabled: true}
			if err := db.UpsertProviderCredential(r.Context(), item); err != nil {
				writeError(w, http.StatusInternalServerError, "credential_store_failed", "could not store provider credential")
				return
			}
			if credentials.Reload != nil {
				if err := credentials.Reload(); err != nil {
					message := sanitizeError(err.Error())
					item.LastError = &message
				}
			}
			writeJSON(w, http.StatusCreated, item)
		default:
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "provider credentials endpoint only accepts GET and POST")
		}
	})
	mux.HandleFunc("/api/providers/credentials/", func(w http.ResponseWriter, r *http.Request) {
		if db == nil || r.Method != http.MethodDelete {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "provider credential deletion only accepts DELETE")
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/api/providers/credentials/")
		if id == "" {
			writeError(w, http.StatusBadRequest, "invalid_request", "provider credential ID is required")
			return
		}
		if err := db.DeleteProviderCredential(r.Context(), id); err != nil {
			writeError(w, http.StatusInternalServerError, "credential_delete_failed", "could not delete provider credential")
			return
		}
		if credentials.Reload != nil {
			_ = credentials.Reload()
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
	})
	mux.Handle("/", ui)
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "models endpoint only accepts GET")
			return
		}
		if catalogManager == nil {
			writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": []any{}})
			return
		}
		models := catalogManager.Snapshot().Models
		data := make([]map[string]any, 0, len(models))
		for _, model := range models {
			data = append(data, map[string]any{"id": model.ID, "object": "model", "owned_by": "paylessforai", "name": model.Name, "free": model.Free, "context_length": model.ContextLength, "max_completion_tokens": model.MaxCompletionTokens, "supported_parameters": model.SupportedParameters})
		}
		writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
	})
	for _, path := range []string{"/v1/chat/completions", "/v1/responses", "/v1/messages", "/anthropic/v1/messages"} {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			if proxyHandler != nil {
				protocol := matcher.ProtocolChatCompletions
				switch r.URL.Path {
				case "/v1/responses":
					protocol = matcher.ProtocolResponses
				case "/v1/messages", "/anthropic/v1/messages":
					protocol = matcher.ProtocolAnthropic
				}
				proxyHandler.ServeHTTP(w, r, protocol)
				return
			}
			if r.Method != http.MethodPost {
				writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "inference endpoint only accepts POST")
				return
			}
			writeError(w, http.StatusNotImplemented, "not_implemented", "provider proxy is not configured yet")
		})
	}
	return &Server{httpServer: &http.Server{Addr: addr, Handler: withRequestID(mux), ReadHeaderTimeout: readHeaderTimeout, IdleTimeout: idleTimeout}}, nil
}

func (s *Server) ListenAndServe() error { return s.httpServer.ListenAndServe() }

func (s *Server) Shutdown(ctx context.Context) error { return s.httpServer.Shutdown(ctx) }

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"type": "payless_error", "code": code, "message": message}})
}

func sanitizeError(message string) string {
	message = strings.Join(strings.Fields(message), " ")
	if len(message) > 1024 {
		return message[:1024]
	}
	return message
}

func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := make([]byte, 16)
		if _, err := rand.Read(id); err != nil {
			id = []byte(time.Now().UTC().Format("20060102150405.000000000"))
		}
		w.Header().Set("X-PayLess-Request-ID", hex.EncodeToString(id))
		next.ServeHTTP(w, r)
	})
}
