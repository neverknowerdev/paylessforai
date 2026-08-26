package controlplane

import (
	"net/http"
)

func (s *Server) registerHealthRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/readyz", s.handleReady)
	mux.HandleFunc("/api/status", s.handleStatus)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (s *Server) handleReady(w http.ResponseWriter, _ *http.Request) {
	if s.db == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ready": false})
		return
	}
	if err := s.db.DB().Ping(); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ready": false, "error": "database unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ready": true})
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	ready := s.db != nil && s.db.DB().Ping() == nil
	status := map[string]any{"ready": ready}
	if s.catalog != nil {
		snapshot := s.catalog.Snapshot()
		status["catalog_updated_at"] = snapshot.UpdatedAt
		status["model_count"] = len(snapshot.Models)
		status["route_count"] = len(snapshot.Routes)
	}
	if s.groups != nil {
		status["group_count"] = len(s.groups.Snapshot())
	}
	writeJSON(w, http.StatusOK, status)
}
