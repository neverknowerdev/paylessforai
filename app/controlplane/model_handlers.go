package controlplane

import (
	"net/http"
	"sort"
)

func modalityNames(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for value, enabled := range values {
		if enabled {
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func (s *Server) registerModelRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/models", s.handleCatalogModels)
}

func (s *Server) handleCatalogModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "model catalog only accepts GET")
		return
	}
	if s.catalog == nil {
		writeJSON(w, http.StatusOK, map[string]any{"data": []any{}})
		return
	}
	routes := s.catalog.Snapshot().Routes
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
			"input_modalities":     modalityNames(route.Capabilities.InputModalities), "output_modalities": modalityNames(route.Capabilities.OutputModalities), "tags": route.Capabilities.Tags,
			"pricing": map[string]any{
				"input": route.Price.InputPicoUSDPerToken, "output": route.Price.OutputPicoUSDPerToken,
				"cached_read": route.Price.CachedReadPicoUSDPerToken, "cache_write": route.Price.CacheWritePicoUSDPerToken,
				"reasoning": route.Price.ReasoningPicoUSDPerToken, "fixed": route.Price.FixedPicoUSD,
			},
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data})
}
