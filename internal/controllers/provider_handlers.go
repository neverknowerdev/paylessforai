package controllers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/neverknowerdev/paylessforai/internal/ids"
	"github.com/neverknowerdev/paylessforai/internal/store"
)

func (s *Server) registerProviderRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/providers/credentials", s.handleProviderCredentials)
	mux.HandleFunc("/api/providers/credentials/", s.handleProviderCredential)
}

func (s *Server) handleProviderCredentials(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "database is unavailable")
		return
	}
	switch r.Method {
	case http.MethodGet:
		items, err := s.db.ListProviderCredentials(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "credential_list_failed", "could not list provider credentials")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": items})
	case http.MethodPost:
		s.createProviderCredential(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "provider credentials endpoint only accepts GET and POST")
	}
}

func (s *Server) createProviderCredential(w http.ResponseWriter, r *http.Request) {
	if s.credentials.Box == nil {
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
	if _, ok := s.credentials.ProviderBases[input.Provider]; !ok {
		writeError(w, http.StatusBadRequest, "unsupported_provider", "provider is not configured")
		return
	}
	ciphertext, nonce, err := s.credentials.Box.Seal(input.APIKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "credential_encrypt_failed", "could not encrypt provider credential")
		return
	}
	item := store.ProviderCredential{ID: ids.New(), Provider: input.Provider, Label: input.Label, Ciphertext: ciphertext, Nonce: nonce, Enabled: true}
	if err := s.db.UpsertProviderCredential(r.Context(), item); err != nil {
		writeError(w, http.StatusInternalServerError, "credential_store_failed", "could not store provider credential")
		return
	}
	if s.credentials.Reload != nil {
		if err := s.credentials.Reload(); err != nil {
			message := sanitizeError(err.Error())
			item.LastError = &message
		}
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) handleProviderCredential(w http.ResponseWriter, r *http.Request) {
	if s.db == nil || r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "provider credential deletion only accepts DELETE")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/providers/credentials/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "provider credential ID is required")
		return
	}
	if err := s.db.DeleteProviderCredential(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "credential_delete_failed", "could not delete provider credential")
		return
	}
	if s.credentials.Reload != nil {
		_ = s.credentials.Reload()
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}
