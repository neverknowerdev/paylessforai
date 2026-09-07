// Package connectors fetches source-specific payloads and emits source-neutral records.
package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/neverknowerdev/paylessforai/internal/statserver/models"
)

type Connector struct {
	Name, DisplayName, URL string
	Key                    string
	Fetch                  func(context.Context, string) ([]models.CatalogRecord, error)
}

func Default(artificialAnalysisKey, openRouterKey, huggingFaceToken, surplusKey string) []Connector {
	return []Connector{
		{Name: "artificial_analysis", DisplayName: "Artificial Analysis", URL: "https://artificialanalysis.ai/api/v2/language/models/free", Key: artificialAnalysisKey, Fetch: artificialAnalysis},
		{Name: "openrouter", DisplayName: "OpenRouter", URL: "https://openrouter.ai/api/v1/models", Key: openRouterKey, Fetch: openRouter},
		{Name: "huggingface", DisplayName: "Hugging Face", URL: "https://huggingface.co/api/models?limit=200&sort=downloads&direction=-1", Key: huggingFaceToken, Fetch: huggingFace},
		{Name: "surplus", DisplayName: "Surplus Intelligence", URL: "https://api.surplusintelligence.ai/v1/models", Key: surplusKey, Fetch: surplus},
	}
}

func artificialAnalysis(ctx context.Context, key string) ([]models.CatalogRecord, error) {
	var response struct {
		Data []struct {
			ID, Name, Slug string
			ModelCreator   struct {
				Name string `json:"name"`
			} `json:"model_creator"`
			Evaluations map[string]float64 `json:"evaluations"`
			Pricing     struct {
				Input  float64 `json:"price_1m_input_tokens"`
				Output float64 `json:"price_1m_output_tokens"`
			} `json:"pricing"`
			Performance map[string]float64 `json:"performance"`
		} `json:"data"`
	}
	if err := getJSON(ctx, "https://artificialanalysis.ai/api/v2/language/models/free", key, "x-api-key", &response); err != nil {
		return nil, err
	}
	records := make([]models.CatalogRecord, 0, len(response.Data))
	for _, item := range response.Data {
		input, output := item.Pricing.Input, item.Pricing.Output
		records = append(records, models.CatalogRecord{SourceID: item.ID, Name: item.Name, Creator: item.ModelCreator.Name, ProviderModel: item.Slug, PriceSource: "Artificial Analysis", PriceSourceURL: "https://artificialanalysis.ai/api/v2/language/models/free", Input: &input, Output: &output, Benchmarks: item.Evaluations, Metadata: map[string]any{"slug": item.Slug, "performance": item.Performance}})
	}
	return records, nil
}

func openRouter(ctx context.Context, key string) ([]models.CatalogRecord, error) {
	var response struct {
		Data []map[string]any `json:"data"`
	}
	if err := getJSON(ctx, "https://openrouter.ai/api/v1/models?output_modalities=all", key, "Authorization", &response); err != nil {
		return nil, err
	}
	records := make([]models.CatalogRecord, 0, len(response.Data))
	for _, item := range response.Data {
		id, _ := item["id"].(string)
		name, _ := item["name"].(string)
		if name == "" {
			name = id
		}
		creator := ""
		if parts := strings.Split(id, "/"); len(parts) > 1 {
			creator = parts[0]
		}
		pricing, _ := item["pricing"].(map[string]any)
		input, output := number(pricing["prompt"])*1e6, number(pricing["completion"])*1e6
		cacheRead, cacheWrite := millionPointer(pricing["cache_read"]), millionPointer(pricing["cache_write"])
		records = append(records, models.CatalogRecord{SourceID: id, Name: name, Creator: creator, ProviderModel: id, PriceSource: "OpenRouter", PriceSourceURL: "https://openrouter.ai/api/v1/models", Input: &input, Output: &output, CacheRead: cacheRead, CacheWrite: cacheWrite, Context: int64(number(item["context_length"])), Metadata: item})
	}
	return records, nil
}

func huggingFace(ctx context.Context, key string) ([]models.CatalogRecord, error) {
	var response []map[string]any
	if err := getJSON(ctx, "https://huggingface.co/api/models?limit=200&sort=downloads&direction=-1", key, "Authorization", &response); err != nil {
		return nil, err
	}
	records := make([]models.CatalogRecord, 0, len(response))
	for _, item := range response {
		id, _ := item["id"].(string)
		if id == "" {
			continue
		}
		name := id
		if candidate, ok := item["modelId"].(string); ok && candidate != "" {
			name = candidate
		}
		creator := strings.Split(id, "/")[0]
		records = append(records, models.CatalogRecord{SourceID: id, Name: name, Creator: creator, Metadata: item})
	}
	return records, nil
}

func surplus(ctx context.Context, key string) ([]models.CatalogRecord, error) {
	var response struct {
		Data []map[string]any `json:"data"`
	}
	if err := getJSON(ctx, "https://api.surplusintelligence.ai/v1/models", key, "Authorization", &response); err != nil {
		return nil, err
	}
	records := make([]models.CatalogRecord, 0, len(response.Data))
	for _, item := range response.Data {
		id, _ := item["id"].(string)
		name, _ := item["name"].(string)
		if name == "" {
			name = id
		}
		input, output := number(item["input_price"]), number(item["output_price"])
		records = append(records, models.CatalogRecord{SourceID: id, Name: name, ProviderModel: id, PriceSource: "Surplus Intelligence", PriceSourceURL: "https://api.surplusintelligence.ai/v1/models", Input: floatPointer(input), Output: floatPointer(output), Metadata: item})
	}
	return records, nil
}

func getJSON(ctx context.Context, url, key, header string, destination any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if key != "" {
		if header == "Authorization" {
			request.Header.Set(header, "Bearer "+key)
		} else {
			request.Header.Set(header, key)
		}
	}
	request.Header.Set("Accept", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("%s: %s", response.Status, string(body))
	}
	return json.NewDecoder(response.Body).Decode(destination)
}

func number(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case json.Number:
		parsed, _ := typed.Float64()
		return parsed
	case string:
		parsed, _ := strconv.ParseFloat(typed, 64)
		return parsed
	}
	return 0
}
func floatPointer(value float64) *float64 { return &value }
func millionPointer(value any) *float64 {
	if value == nil {
		return nil
	}
	parsed := number(value) * 1e6
	return &parsed
}
