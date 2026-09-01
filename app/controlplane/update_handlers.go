package controlplane

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/neverknowerdev/paylessforai/internal/updater"
)

func (s *Server) registerUpdateRoutes(mux *http.ServeMux) {
	if s.credentials.Updates == nil {
		return
	}
	mux.HandleFunc("/api/updates", s.handleUpdates)
	mux.HandleFunc("/api/updates/settings", s.handleUpdateSettings)
	mux.HandleFunc("/api/updates/check", s.handleUpdateCheck)
	mux.HandleFunc("/api/updates/install", s.handleUpdateInstall)
	mux.HandleFunc("/api/updates/warning/acknowledge", s.handleUpdateWarningAcknowledge)
	mux.HandleFunc("/api/updates/history", s.handleUpdateHistory)
}

func (s *Server) handleUpdates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	payload, err := s.credentials.Updates.Snapshot(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var settings updater.Settings
	if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid settings"})
		return
	}
	if err := s.credentials.Updates.SaveSettings(r.Context(), settings); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	go func() { _ = s.credentials.Updates.Check(context.Background(), false) }()
	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true})
}

func (s *Server) handleUpdateInstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Version string `json:"version"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	go func() { _ = s.credentials.Updates.Install(context.Background(), body.Version) }()
	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true})
}

func (s *Server) handleUpdateWarningAcknowledge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if err := s.credentials.Updates.AcknowledgeWarning(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"acknowledged": true})
}

func (s *Server) handleUpdateHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	payload, err := s.credentials.Updates.Snapshot(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": payload.History})
}
