package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/neverknowerdev/paylessforai/internal/matcher"
	"github.com/neverknowerdev/paylessforai/internal/retry"
)

type HTTPClient struct {
	Provider string
	BaseURL  string
	APIKey   string
	Client   *http.Client
}

func NewHTTPClient(provider, baseURL, apiKey string) *HTTPClient {
	return &HTTPClient{Provider: provider, BaseURL: strings.TrimRight(baseURL, "/"), APIKey: apiKey, Client: &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, MaxIdleConns: 32, MaxIdleConnsPerHost: 8, IdleConnTimeout: 90 * time.Second}}}
}

func (c *HTTPClient) Name() string { return c.Provider }

func (c *HTTPClient) Discover(ctx context.Context) ([]Model, error) {
	path := "/models"
	if c.Provider == "openrouter" && c.APIKey != "" {
		path = "/models/user"
	}
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
		cachedRead, _ := firstPrice(item.Pricing, "cache_read", "cacheRead", "cache_read_input")
		cacheWrite, _ := firstPrice(item.Pricing, "cache_write", "cacheWrite", "cache_creation_input")
		reasoning, _ := firstPrice(item.Pricing, "reasoning", "thinking")
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
		models = append(models, Model{ID: item.ID, Name: item.Name, Free: free, ContextLength: item.ContextLength, MaxCompletionTokens: item.MaxCompletionTokens, Pricing: matcher.Price{InputPicoUSDPerToken: input, OutputPicoUSDPerToken: output, CachedReadPicoUSDPerToken: cachedRead, CacheWritePicoUSDPerToken: cacheWrite, ReasoningPicoUSDPerToken: reasoning, FixedPicoUSD: fixed, ObservedAt: time.Now().UTC()}, PriceAvailable: inputOK && outputOK, SupportedParameters: item.SupportedParameters, InputModalities: normalizeTags(inputModalities), OutputModalities: normalizeTags(outputModalities), Tags: normalizeTags(tags)})
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
	output := []string{"text"}
	return normalizeTags(input), output
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
	if strings.HasSuffix(strings.ToLower(id), ":free") || strings.HasSuffix(strings.ToLower(id), "-free") || strings.Contains(label, "(free)") || strings.Contains(strings.ToLower(description), "free") || (provider == "openrouter" && id == "openrouter/free") {
		return true
	}
	if provider == "openrouter" {
		return priced && input == 0 && output == 0
	}
	// Surplus can mirror catalog entries whose token fields are zero because
	// their non-token meter is omitted. Treat only an explicit free label as
	// free there; a bare zero price is not enough evidence.
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

func (c *HTTPClient) Do(ctx context.Context, protocol matcher.Protocol, model string, body []byte) (*http.Response, error) {
	if protocol == matcher.ProtocolAnthropic {
		if c.Provider == "surplus" {
			return c.doRequest(ctx, strings.TrimSuffix(c.BaseURL, "/v1")+"/anthropic/v1/messages", model, body)
		}
		return c.doRequest(ctx, c.BaseURL+"/messages", model, body)
	}
	path := "/chat/completions"
	if protocol == matcher.ProtocolResponses {
		path = "/responses"
	}
	return c.doRequest(ctx, c.BaseURL+path, model, body)
}

func (c *HTTPClient) doRequest(ctx context.Context, url, model string, body []byte) (*http.Response, error) {
	rewritten, err := rewriteModel(body, model)
	if err != nil {
		return nil, fmt.Errorf("rewrite upstream model: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(rewritten)))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	c.addHeaders(request)
	response, err := c.Client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		upstreamErr := c.readUpstreamError(response)
		response.Body.Close()
		return nil, upstreamErr
	}
	return response, nil
}

func (c *HTTPClient) addHeaders(request *http.Request) {
	if c.APIKey != "" {
		request.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	request.Header.Set("Accept", "application/json, text/event-stream")
}

func (c *HTTPClient) readUpstreamError(response *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(response.Body, 32<<10))
	class := retry.ErrorUnknown
	switch {
	case response.StatusCode == http.StatusBadRequest:
		class = retry.ErrorInvalidRequest
	case response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden:
		class = retry.ErrorAuthentication
	case response.StatusCode == http.StatusPaymentRequired:
		class = retry.ErrorPayment
	case response.StatusCode == http.StatusNotFound:
		class = retry.ErrorModelNotFound
	case response.StatusCode == http.StatusTooManyRequests:
		class = retry.ErrorRateLimit
	case response.StatusCode == http.StatusRequestTimeout:
		class = retry.ErrorTimeout
	case response.StatusCode >= 500:
		class = retry.ErrorServer
	}
	message := strings.TrimSpace(string(body))
	if message == "" {
		message = response.Status
	}
	return &UpstreamError{Provider: c.Provider, StatusCode: response.StatusCode, Class: class, Message: message, RetryAfter: retryAfterSeconds(response)}
}

func rewriteModel(body []byte, model string) ([]byte, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(model)
	if err != nil {
		return nil, err
	}
	payload["model"] = encoded
	return json.Marshal(payload)
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

func retryAfterSeconds(response *http.Response) *int {
	value := response.Header.Get("Retry-After")
	if seconds, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && seconds >= 0 {
		return &seconds
	}
	if when, err := http.ParseTime(value); err == nil {
		remaining := time.Until(when)
		if remaining <= 0 {
			seconds := 0
			return &seconds
		}
		seconds := int((remaining + time.Second - 1) / time.Second)
		return &seconds
	}
	return nil
}
