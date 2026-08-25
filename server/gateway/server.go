// Package gateway contains the hosted public inference surface. It is kept
// independent from the control-plane UI so the two can be placed behind
// different listeners or access policies in a later deployment.
package gateway

import (
	"encoding/json"
	"net/http"

	"github.com/neverknowerdev/paylessforai/internal/catalog"
	"github.com/neverknowerdev/paylessforai/internal/matcher"
	proxyservice "github.com/neverknowerdev/paylessforai/internal/proxy"
)

// NewHandler builds the public models and inference handler. Authentication,
// routing, retries, usage recording, and provider failover remain in the
// shared proxy service used by the hosted server.
func NewHandler(catalogManager *catalog.Manager, proxyHandler *proxyservice.Proxy) http.Handler {
	mux := http.NewServeMux()
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
			data = append(data, map[string]any{
				"id": model.ID, "object": "model", "owned_by": "paylessforai", "name": model.Name,
				"free": model.Free, "context_length": model.ContextLength,
				"max_completion_tokens": model.MaxCompletionTokens,
				"supported_parameters":  model.SupportedParameters,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
	})
	for _, path := range []string{"/v1/chat/completions", "/v1/responses", "/v1/messages", "/anthropic/v1/messages"} {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			if proxyHandler == nil {
				if r.Method != http.MethodPost {
					writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "inference endpoint only accepts POST")
					return
				}
				writeError(w, http.StatusNotImplemented, "not_implemented", "provider proxy is not configured yet")
				return
			}
			protocol := matcher.ProtocolChatCompletions
			switch r.URL.Path {
			case "/v1/responses":
				protocol = matcher.ProtocolResponses
			case "/v1/messages", "/anthropic/v1/messages":
				protocol = matcher.ProtocolAnthropic
			}
			proxyHandler.ServeHTTP(w, r, protocol)
		})
	}
	return mux
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"type": code, "message": message}})
}
