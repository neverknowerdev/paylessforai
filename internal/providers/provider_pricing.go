package providers

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strings"
)

type surplusMarket struct {
	Model                 string  `json:"model"`
	BestInputPer1M        float64 `json:"best_input_per_1m"`
	BestOutputPer1M       float64 `json:"best_output_per_1m"`
	BestCacheReadPer1M    float64 `json:"best_cache_read_per_1m"`
	BestCacheWritePer1M   float64 `json:"best_cache_write_per_1m"`
	DirectInputPer1M      float64 `json:"direct_input_per_1m"`
	DirectOutputPer1M     float64 `json:"direct_output_per_1m"`
	DirectCacheReadPer1M  float64 `json:"direct_cache_read_per_1m"`
	DirectCacheWritePer1M float64 `json:"direct_cache_write_per_1m"`
}

// applySurplusMarketPricing overlays Surplus's live market and direct
// reference prices onto its discovered model catalog.
func applySurplusMarketPricing(ctx context.Context, client *HTTPClient, models []Model) {
	baseURL := strings.TrimSuffix(client.BaseURL, "/v1")
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/markets", nil)
	if err != nil {
		return
	}
	client.addHeaders(request)
	response, err := client.Client.Do(request)
	if err != nil {
		return
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return
	}
	var payload struct {
		Markets []surplusMarket `json:"markets"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(&payload); err != nil {
		return
	}
	byModel := make(map[string]surplusMarket, len(payload.Markets))
	for _, market := range payload.Markets {
		byModel[market.Model] = market
	}
	for index := range models {
		market, ok := byModel[models[index].ID]
		if !ok {
			continue
		}
		if market.BestInputPer1M > 0 {
			models[index].Pricing.InputPicoUSDPerToken = int64(math.Round(market.BestInputPer1M))
		}
		if market.BestOutputPer1M > 0 {
			models[index].Pricing.OutputPicoUSDPerToken = int64(math.Round(market.BestOutputPer1M))
		}
		if market.BestCacheReadPer1M > 0 {
			models[index].Pricing.CachedReadPicoUSDPerToken = int64(math.Round(market.BestCacheReadPer1M))
		}
		if market.BestCacheWritePer1M > 0 {
			models[index].Pricing.CacheWritePicoUSDPerToken = int64(math.Round(market.BestCacheWritePer1M))
		}
		if market.DirectInputPer1M > 0 {
			models[index].OfficialPricing.InputPicoUSDPerToken = int64(math.Round(market.DirectInputPer1M))
		}
		if market.DirectOutputPer1M > 0 {
			models[index].OfficialPricing.OutputPicoUSDPerToken = int64(math.Round(market.DirectOutputPer1M))
		}
		if market.DirectCacheReadPer1M > 0 {
			models[index].OfficialPricing.CachedReadPicoUSDPerToken = int64(math.Round(market.DirectCacheReadPer1M))
		}
		if market.DirectCacheWritePer1M > 0 {
			models[index].OfficialPricing.CacheWritePicoUSDPerToken = int64(math.Round(market.DirectCacheWritePer1M))
		}
		models[index].OfficialPriceAvailable = market.DirectInputPer1M > 0 && market.DirectOutputPer1M > 0
		models[index].PriceAvailable = models[index].Pricing.InputPicoUSDPerToken > 0 && models[index].Pricing.OutputPicoUSDPerToken > 0
	}
}
