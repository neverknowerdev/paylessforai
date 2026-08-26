package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/neverknowerdev/paylessforai/internal/matcher"
)

// Discover loads a provider catalog and applies any provider-specific pricing
// enrichment before returning routes to the catalog manager.
func (c *HTTPClient) Discover(ctx context.Context) ([]Model, error) {
	var failures []string
	for _, path := range c.modelPaths() {
		models, err := c.discoverPath(ctx, path)
		if err != nil {
			failures = append(failures, path+": "+err.Error())
			continue
		}
		if c.Provider == "surplus" {
			applySurplusMarketPricing(ctx, c, models)
		}
		if c.Provider == "openrouter" {
			enrichOpenRouterDiscounts(ctx, c, models)
		}
		return models, nil
	}
	return nil, fmt.Errorf("discover %s models: %s", c.Provider, strings.Join(failures, "; "))
}

func (c *HTTPClient) modelPaths() []string {
	paths := make([]string, 0, 5)
	// The public catalog is the complete model list. OpenRouter's user-scoped
	// catalog is a useful fallback for installations where the public list is
	// unavailable, but it can be filtered by account preferences and therefore
	// must not be preferred when building our catalog.
	modelsPath := "/models"
	if c.Provider == "openrouter" {
		// OpenRouter defaults this endpoint to text-output models; explicitly
		// request every output modality so audio, image, and other routes are
		// not silently omitted from the catalog.
		modelsPath += "?output_modalities=all"
	}
	paths = append(paths, modelsPath)
	if c.Provider == "openrouter" && c.APIKey != "" {
		paths = append(paths, "/models/user?output_modalities=all")
	}
	paths = append(paths, "/v1/models", "/api/v1/models", "/api/models")
	seen := make(map[string]struct{}, len(paths))
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		result = append(result, path)
	}
	return result
}

func (c *HTTPClient) discoverPath(ctx context.Context, path string) ([]Model, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	c.addHeaders(request)
	response, err := c.Client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, c.readUpstreamError(response)
	}
	var payload struct {
		Data []struct {
			ID                  string                     `json:"id"`
			Name                string                     `json:"name"`
			Description         string                     `json:"description"`
			ContextLength       int64                      `json:"context_length"`
			MaxCompletionTokens int64                      `json:"max_completion_tokens"`
			Pricing             map[string]json.RawMessage `json:"pricing"`
			SupportedParameters []string                   `json:"supported_parameters"`
			Architecture        struct {
				InputModalities  []string `json:"input_modalities"`
				OutputModalities []string `json:"output_modalities"`
				Modality         string   `json:"modality"`
			} `json:"architecture"`
			SupportedFeatures []string `json:"supported_features"`
			Tags              []string `json:"tags"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode %s models: %w", c.Provider, err)
	}
	models := make([]Model, 0, len(payload.Data))
	for _, item := range payload.Data {
		input, inputOK := firstPrice(item.Pricing, "prompt", "input")
		output, outputOK := firstPrice(item.Pricing, "completion", "output")
		cachedRead, _ := firstPrice(item.Pricing, "cache_read", "cacheRead", "cache_read_input", "input_cache_read")
		cacheWrite, _ := firstPrice(item.Pricing, "cache_write", "cacheWrite", "cache_creation_input", "input_cache_write")
		reasoning, _ := firstPrice(item.Pricing, "reasoning", "thinking", "internal_reasoning")
		fixed, _ := firstPrice(item.Pricing, "request", "fixed", "per_request")
		free := isFreeModel(c.Provider, item.ID, item.Name, item.Description, input, output, inputOK && outputOK && !hasNonTokenCharge(item.Pricing))
		if free {
			if !inputOK {
				input, inputOK = 0, true
			}
			if !outputOK {
				output, outputOK = 0, true
			}
		}
		inputModalities, outputModalities := item.Architecture.InputModalities, item.Architecture.OutputModalities
		if len(inputModalities) == 0 || len(outputModalities) == 0 {
			inputModalities, outputModalities = modalitiesFromDescriptor(item.Architecture.Modality)
		}
		if len(inputModalities) == 0 {
			inputModalities, outputModalities = modalitiesFromTags(item.Tags)
		}
		tags := append(append([]string(nil), item.SupportedFeatures...), item.Tags...)
		pricing := matcher.Price{InputPicoUSDPerToken: input, OutputPicoUSDPerToken: output, CachedReadPicoUSDPerToken: cachedRead, CacheWritePicoUSDPerToken: cacheWrite, ReasoningPicoUSDPerToken: reasoning, FixedPicoUSD: fixed, ObservedAt: time.Now().UTC()}
		models = append(models, Model{ID: item.ID, Name: item.Name, Free: free, ContextLength: item.ContextLength, MaxCompletionTokens: item.MaxCompletionTokens, Pricing: pricing, PriceAvailable: inputOK && outputOK, OfficialPricing: pricing, OfficialPriceAvailable: inputOK && outputOK, SupportedParameters: item.SupportedParameters, InputModalities: normalizeTags(inputModalities), OutputModalities: normalizeTags(outputModalities), Tags: normalizeTags(tags)})
	}
	return models, nil
}

func modalitiesFromDescriptor(value string) ([]string, []string) {
	parts := strings.Split(value, "->")
	if len(parts) != 2 {
		return nil, nil
	}
	return normalizeTags(strings.Split(parts[0], "+")), normalizeTags(strings.Split(parts[1], "+"))
}

func modalitiesFromTags(values []string) ([]string, []string) {
	known := map[string]bool{"text": true, "image": true, "audio": true, "video": true}
	input := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if known[value] {
			input = append(input, value)
		}
	}
	if len(input) == 0 {
		return nil, nil
	}
	return normalizeTags(input), []string{"text"}
}

func normalizeTags(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func isFreeModel(provider, id, name, description string, input, output int64, priced bool) bool {
	label := strings.ToLower(strings.TrimSpace(id + " " + name))
	if provider == "openrouter" {
		if strings.HasSuffix(strings.ToLower(id), ":free") || strings.HasSuffix(strings.ToLower(id), "-free") || strings.Contains(label, "(free)") || strings.Contains(strings.ToLower(description), "free") || id == "openrouter/free" {
			return true
		}
		return priced && input == 0 && output == 0
	}
	if provider == "surplus" {
		labelledFree := strings.HasSuffix(strings.ToLower(id), ":free") || strings.HasSuffix(strings.ToLower(id), "-free") || strings.Contains(label, "(free)") || strings.Contains(strings.ToLower(description), "free")
		return labelledFree && priced && input == 0 && output == 0
	}
	return false
}

// hasNonTokenCharge prevents media models with zero token prices but a paid
// per-image, per-minute, or per-job meter from being presented as free LLMs.
func hasNonTokenCharge(pricing map[string]json.RawMessage) bool {
	for name, raw := range pricing {
		switch strings.ToLower(name) {
		case "prompt", "input", "completion", "output", "cache_read", "cacheread", "cache_write", "cachewrite", "cache_creation_input", "reasoning", "thinking":
			continue
		}
		var value string
		if json.Unmarshal(raw, &value) != nil {
			var number json.Number
			if json.Unmarshal(raw, &number) != nil {
				continue
			}
			value = number.String()
		}
		amount, ok := new(big.Float).SetString(value)
		if ok && amount.Sign() > 0 {
			return true
		}
	}
	return false
}

func firstPrice(pricing map[string]json.RawMessage, names ...string) (int64, bool) {
	for _, name := range names {
		value, ok := pricing[name]
		if !ok {
			continue
		}
		var stringValue string
		if json.Unmarshal(value, &stringValue) == nil {
			if pico, ok := picoPerToken(stringValue); ok {
				return pico, true
			}
			continue
		}
		var numberValue json.Number
		if json.Unmarshal(value, &numberValue) == nil {
			if pico, ok := picoPerToken(numberValue.String()); ok {
				return pico, true
			}
		}
	}
	return 0, false
}

func picoPerToken(value string) (int64, bool) {
	if value == "" {
		return 0, false
	}
	rational, ok := new(big.Rat).SetString(value)
	if !ok || rational.Sign() < 0 {
		return 0, false
	}
	rational.Mul(rational, big.NewRat(1_000_000_000_000, 1))
	if !rational.IsInt() || !rational.Num().IsInt64() {
		return 0, false
	}
	return rational.Num().Int64(), true
}
