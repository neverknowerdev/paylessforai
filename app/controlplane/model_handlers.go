package controlplane

import (
	"net/http"
	"sort"
	"strings"

	"github.com/neverknowerdev/paylessforai/internal/matcher"
)

type routeDiscount struct {
	MaxBPS    int64
	InputBPS  int64
	OutputBPS int64
	Available bool
}

func catalogDiscounts(routes []matcher.Route) map[string]routeDiscount {
	type baseline struct{ input, output int64 }
	official := make(map[string]baseline)
	for _, route := range routes {
		if route.Provider != "openrouter" || strings.HasSuffix(strings.ToLower(route.UpstreamModel), ":free") || !route.PriceAvailable || (route.Price.InputPicoUSDPerToken <= 0 && route.Price.OutputPicoUSDPerToken <= 0) {
			continue
		}
		if _, exists := official[route.LogicalModel]; !exists {
			official[route.LogicalModel] = baseline{input: route.Price.InputPicoUSDPerToken, output: route.Price.OutputPicoUSDPerToken}
		}
	}
	result := make(map[string]routeDiscount)
	for _, route := range routes {
		base, ok := official[route.LogicalModel]
		if !ok || !route.PriceAvailable || (!route.Free && route.Price.InputPicoUSDPerToken <= 0 && route.Price.OutputPicoUSDPerToken <= 0) {
			continue
		}
		values := make([]int64, 0, 2)
		discount := routeDiscount{Available: true}
		if base.input > 0 {
			discount.InputBPS = (base.input - route.Price.InputPicoUSDPerToken) * 10000 / base.input
			values = append(values, discount.InputBPS)
		}
		if base.output > 0 {
			discount.OutputBPS = (base.output - route.Price.OutputPicoUSDPerToken) * 10000 / base.output
			values = append(values, discount.OutputBPS)
		}
		if len(values) == 0 {
			continue
		}
		discount.MaxBPS = values[0]
		for _, value := range values[1:] {
			if value > discount.MaxBPS {
				discount.MaxBPS = value
			}
		}
		result[route.Provider+"\x00"+route.LogicalModel+"\x00"+route.UpstreamModel] = discount
	}
	return result
}

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
	discounts := catalogDiscounts(routes)
	data := make([]map[string]any, 0, len(routes))
	seen := make(map[string]struct{}, len(routes))
	for _, route := range routes {
		key := route.Provider + "\x00" + route.LogicalModel + "\x00" + route.UpstreamModel
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		item := map[string]any{
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
		}
		if discount, ok := discounts[key]; ok && discount.Available {
			item["discount_percent_bps"] = discount.MaxBPS
			item["discount_input_percent_bps"] = discount.InputBPS
			item["discount_output_percent_bps"] = discount.OutputBPS
			item["discount_source"] = "openrouter"
		}
		data = append(data, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data})
}
