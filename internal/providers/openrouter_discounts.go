package providers

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/neverknowerdev/paylessforai/internal/matcher"
)

const (
	openRouterDiscountWorkers = 8
	openRouterDiscountTimeout = 15 * time.Second
)

type openRouterEndpoint struct {
	Pricing map[string]json.RawMessage `json:"pricing"`
}

type openRouterDiscountCandidate struct {
	Discount float64
	Pricing  matcher.Price
}

// enrichOpenRouterDiscounts loads endpoint-level promotional discounts. The
// public models endpoint exposes the effective price but not the discount
// percentage, while endpoint metadata contains both the effective price and
// pricing.discount. A bounded worker pool keeps refreshes predictable for the
// large OpenRouter catalog.
func enrichOpenRouterDiscounts(parent context.Context, client *HTTPClient, models []Model) {
	if len(models) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(parent, openRouterDiscountTimeout)
	defer cancel()

	jobs := make(chan int)
	results := make(chan struct {
		index int
		model Model
		ok    bool
	}, len(models))
	workers := openRouterDiscountWorkers
	if len(models) < workers {
		workers = len(models)
	}
	var waitGroup sync.WaitGroup
	waitGroup.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func() {
			defer waitGroup.Done()
			for index := range jobs {
				model, ok := client.openRouterModelDiscount(ctx, models[index])
				if ok {
					results <- struct {
						index int
						model Model
						ok    bool
					}{index: index, model: model, ok: true}
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for index := range models {
			select {
			case jobs <- index:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		waitGroup.Wait()
		close(results)
	}()
	for result := range results {
		if result.ok {
			models[result.index] = result.model
		}
	}
}

func (c *HTTPClient) openRouterModelDiscount(ctx context.Context, model Model) (Model, bool) {
	parts := strings.SplitN(model.ID, "/", 2)
	if len(parts) != 2 {
		return model, false
	}
	endpointURL := c.BaseURL + "/models/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1]) + "/endpoints"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpointURL, nil)
	if err != nil {
		return model, false
	}
	c.addHeaders(request)
	response, err := c.Client.Do(request)
	if err != nil {
		return model, false
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return model, false
	}
	var payload struct {
		Data struct {
			Endpoints []openRouterEndpoint `json:"endpoints"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return model, false
	}
	var best *openRouterDiscountCandidate
	for _, endpoint := range payload.Data.Endpoints {
		discount, ok := openRouterDiscount(endpoint.Pricing["discount"])
		if !ok || discount <= 0 || discount >= 1 {
			continue
		}
		pricing := matcher.Price{}
		pricing.InputPicoUSDPerToken, _ = firstPrice(endpoint.Pricing, "prompt", "input")
		pricing.OutputPicoUSDPerToken, _ = firstPrice(endpoint.Pricing, "completion", "output")
		pricing.CachedReadPicoUSDPerToken, _ = firstPrice(endpoint.Pricing, "input_cache_read", "cache_read", "cacheRead")
		pricing.CacheWritePicoUSDPerToken, _ = firstPrice(endpoint.Pricing, "input_cache_write", "cache_write", "cacheWrite")
		pricing.ReasoningPicoUSDPerToken, _ = firstPrice(endpoint.Pricing, "internal_reasoning", "reasoning", "thinking")
		if !sameEffectiveTokenPrice(model.Pricing, pricing) {
			continue
		}
		if best == nil || discount > best.Discount {
			candidate := openRouterDiscountCandidate{Discount: discount, Pricing: pricing}
			best = &candidate
		}
	}
	if best == nil {
		return model, false
	}
	model.OfficialPricing = officialPriceFromDiscount(model.Pricing, best.Discount)
	model.OfficialPriceAvailable = model.Pricing.InputPicoUSDPerToken > 0 && model.Pricing.OutputPicoUSDPerToken > 0
	return model, true
}

func openRouterDiscount(raw json.RawMessage) (float64, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, false
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil {
		return 0, false
	}
	value, err := strconv.ParseFloat(number.String(), 64)
	return value, err == nil && value >= 0 && value < 1
}

func sameEffectiveTokenPrice(current, endpoint matcher.Price) bool {
	matched := false
	for _, pair := range [][2]int64{
		{current.InputPicoUSDPerToken, endpoint.InputPicoUSDPerToken},
		{current.OutputPicoUSDPerToken, endpoint.OutputPicoUSDPerToken},
	} {
		if pair[0] > 0 || pair[1] > 0 {
			matched = true
			if pair[0] != pair[1] {
				return false
			}
		}
	}
	return matched
}

func officialPriceFromDiscount(current matcher.Price, discount float64) matcher.Price {
	result := current
	result.InputPicoUSDPerToken = inflateDiscountedPrice(current.InputPicoUSDPerToken, discount)
	result.OutputPicoUSDPerToken = inflateDiscountedPrice(current.OutputPicoUSDPerToken, discount)
	result.CachedReadPicoUSDPerToken = inflateDiscountedPrice(current.CachedReadPicoUSDPerToken, discount)
	result.CacheWritePicoUSDPerToken = inflateDiscountedPrice(current.CacheWritePicoUSDPerToken, discount)
	result.ReasoningPicoUSDPerToken = inflateDiscountedPrice(current.ReasoningPicoUSDPerToken, discount)
	return result
}

func inflateDiscountedPrice(current int64, discount float64) int64 {
	if current <= 0 || discount <= 0 || discount >= 1 {
		return current
	}
	return int64(math.Round(float64(current) / (1 - discount)))
}
