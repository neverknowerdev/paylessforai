package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/neverknowerdev/paylessforai/internal/ids"
	"github.com/neverknowerdev/paylessforai/internal/providers"
	"github.com/neverknowerdev/paylessforai/internal/store"
)

func (s *Server) registerProviderRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/providers", s.handleProviders)
	mux.HandleFunc("/api/providers/credentials", s.handleProviderCredentials)
	mux.HandleFunc("/api/providers/credentials/", s.handleProviderCredential)
}

func (s *Server) handleProviders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "providers endpoint only accepts GET")
		return
	}
	if s.credentials.Registry == nil {
		writeJSON(w, http.StatusOK, map[string]any{"data": []any{}})
		return
	}
	data := make([]map[string]string, 0)
	for _, definition := range s.credentials.Registry.Definitions() {
		data = append(data, map[string]string{"name": definition.Name, "display_name": definition.DisplayName, "default_base_url": definition.DefaultBaseURL})
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data})
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
		Provider     string                  `json:"provider"`
		Label        string                  `json:"label"`
		APIKey       string                  `json:"api_key"`
		BaseURL      string                  `json:"base_url"`
		ManualModels []providers.ManualModel `json:"manual_models"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input); err != nil || strings.TrimSpace(input.Provider) == "" || strings.TrimSpace(input.APIKey) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "provider, label, and api_key are required")
		return
	}
	input.Provider = strings.ToLower(strings.TrimSpace(input.Provider))
	input.BaseURL = strings.TrimRight(strings.TrimSpace(input.BaseURL), "/")
	if s.credentials.Registry == nil {
		writeError(w, http.StatusServiceUnavailable, "provider_registry_unavailable", "provider registry is unavailable")
		return
	}
	client, _, err := s.credentials.Registry.Resolve(input.Provider, input.BaseURL, input.APIKey)
	if err != nil {
		writeError(w, http.StatusBadRequest, "unsupported_provider", err.Error())
		return
	}

	verificationCtx, cancelVerification := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancelVerification()
	discovered, discoveryErr := client.Discover(verificationCtx)
	if len(discovered) == 0 && len(input.ManualModels) == 0 {
		message := "provider returned no models"
		status := http.StatusUnprocessableEntity
		code := "provider_no_models"
		if discoveryErr != nil {
			message = "provider verification failed: " + sanitizeError(discoveryErr.Error())
			status = http.StatusBadGateway
			code = "provider_verification_failed"
		}
		writeJSON(w, status, map[string]any{"error": map[string]any{"code": code, "message": message, "can_enter_models": true, "provider": input.Provider}})
		return
	}
	verifiedManual := make([]providers.Model, 0, len(input.ManualModels))
	if len(input.ManualModels) > 0 {
		verifier, ok := client.(providers.ModelVerifier)
		if !ok {
			writeError(w, http.StatusUnprocessableEntity, "manual_model_verification_unavailable", "this provider cannot verify manually entered models")
			return
		}
		verifiedManual, err = verifier.VerifyModels(verificationCtx, input.ManualModels)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": map[string]any{"code": "manual_model_verification_failed", "message": sanitizeError(err.Error()), "can_enter_models": true, "provider": input.Provider}})
			return
		}
	}
	manualJSON, err := json.Marshal(input.ManualModels)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_manual_models", "manual model definitions are invalid")
		return
	}
	ciphertext, nonce, err := s.credentials.Box.Seal(input.APIKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "credential_encrypt_failed", "could not encrypt provider credential")
		return
	}
	item := store.ProviderCredential{ID: ids.New(), Provider: input.Provider, Label: input.Label, BaseURL: input.BaseURL, Ciphertext: ciphertext, Nonce: nonce, Enabled: true, ManualModelsJSON: string(manualJSON)}
	if err := s.db.UpsertProviderCredential(r.Context(), item); err != nil {
		writeError(w, http.StatusInternalServerError, "credential_store_failed", "could not store provider credential")
		return
	}
	if s.credentials.Reload != nil {
		if err := s.credentials.Reload(); err != nil {
			_ = s.db.DeleteProviderCredential(r.Context(), item.ID)
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": map[string]any{"code": "provider_catalog_refresh_failed", "message": sanitizeError(err.Error()), "provider": input.Provider}})
			return
		}
	}
	writeJSON(w, http.StatusCreated, map[string]any{"data": item, "models_discovered": len(discovered), "models_verified": len(verifiedManual)})
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
