package controlplane

import (
	"net/http"
	"strconv"
)

func (s *Server) registerStatsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/requests", s.handleRequests)
	mux.HandleFunc("/api/stats/summary", s.handleStatsSummary)
	mux.HandleFunc("/api/stats/models", s.handleStatsModels)
	mux.HandleFunc("/api/stats/providers", s.handleStatsProviders)
	mux.HandleFunc("/api/stats/subscriptions", s.handleStatsSubscriptions)
}

func (s *Server) handleStatsSubscriptions(w http.ResponseWriter, r *http.Request) {
	if s.db == nil || r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "subscription statistics only accepts GET")
		return
	}
	items, err := s.db.SubscriptionPricing(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "subscription_stats_failed", "could not load subscription pricing")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items})
}

func (s *Server) handleStatsModels(w http.ResponseWriter, r *http.Request) {
	if s.db == nil || r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "model statistics only accepts GET")
		return
	}
	freeModels := make(map[string]bool)
	if s.catalog != nil {
		for _, route := range s.catalog.Snapshot().Routes {
			if route.Free {
				freeModels[route.LogicalModel] = true
			}
		}
	}
	items, err := s.db.ModelStats(r.Context(), freeModels)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "model_stats_failed", "could not load model statistics")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items})
}

func (s *Server) handleStatsProviders(w http.ResponseWriter, r *http.Request) {
	if s.db == nil || r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "provider statistics only accepts GET")
		return
	}
	items, err := s.db.ProviderStats(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "provider_stats_failed", "could not load provider statistics")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items})
}

func (s *Server) handleRequests(w http.ResponseWriter, r *http.Request) {
	if s.db == nil || r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "request statistics only accepts GET")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := s.db.ListRequestStats(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "request_stats_failed", "could not list request statistics")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items})
}

func (s *Server) handleStatsSummary(w http.ResponseWriter, r *http.Request) {
	if s.db == nil || r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "statistics summary only accepts GET")
		return
	}
	summary, err := s.db.RequestStatsSummary(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "stats_summary_failed", "could not load statistics summary")
		return
	}
	writeJSON(w, http.StatusOK, summary)
}
