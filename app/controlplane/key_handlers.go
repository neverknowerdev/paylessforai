package controlplane

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

func (s *Server) registerKeyRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/client-keys", s.handleClientKeys)
	mux.HandleFunc("/api/client-keys/", s.handleClientKey)
}

func (s *Server) handleClientKeys(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "database is unavailable")
		return
	}
	switch r.Method {
	case http.MethodGet:
		keys, err := s.db.ListClientKeys(r.Context())
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
		key, secret, err := s.db.CreateClientKey(r.Context(), input.Label)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "key_create_failed", "could not create client key")
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"key": key, "secret": secret})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "client keys endpoint only accepts GET and POST")
	}
}

func (s *Server) handleClientKey(w http.ResponseWriter, r *http.Request) {
	if s.db == nil || r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "client key deletion only accepts DELETE")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/client-keys/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "client key ID is required")
		return
	}
	if err := s.db.RevokeClientKey(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "key_revoke_failed", "could not revoke client key")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revoked": true})
}
