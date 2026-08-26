package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/neverknowerdev/paylessforai/internal/matcher"
	"github.com/neverknowerdev/paylessforai/internal/retry"
)

// HTTPClient owns the provider-neutral HTTP transport used by provider
// clients. Catalog discovery and pricing enrichment live in separate files so
// this type remains focused on request execution and upstream errors.
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
	lower := strings.ToLower(string(body))
	quota := strings.Contains(lower, "quota") || strings.Contains(lower, "usage limit") || strings.Contains(lower, "billing cycle") || strings.Contains(lower, "5-hour") || strings.Contains(lower, "5 hour") || strings.Contains(lower, "monthly limit") || strings.Contains(lower, "weekly limit") || strings.Contains(lower, "resource_exhausted")
	switch {
	case response.StatusCode == http.StatusBadRequest:
		class = retry.ErrorInvalidRequest
	case quota:
		class = retry.ErrorQuotaExhausted
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
	var next *time.Time
	if reset := response.Header.Get("X-RateLimit-Reset"); reset != "" {
		if unix, err := strconv.ParseInt(strings.TrimSpace(reset), 10, 64); err == nil && unix > 0 {
			value := time.Unix(unix, 0).UTC()
			next = &value
		}
	}
	if next == nil {
		if reset := response.Header.Get("RateLimit-Reset"); reset != "" {
			if seconds, err := strconv.Atoi(strings.TrimSpace(reset)); err == nil && seconds >= 0 {
				value := time.Now().UTC().Add(time.Duration(seconds) * time.Second)
				next = &value
			}
		}
	}
	if next == nil && class == retry.ErrorQuotaExhausted {
		if seconds := retryAfterSeconds(response); seconds != nil {
			value := time.Now().UTC().Add(time.Duration(*seconds) * time.Second)
			next = &value
		}
	}
	return &UpstreamError{Provider: c.Provider, StatusCode: response.StatusCode, Class: class, Message: message, RetryAfter: retryAfterSeconds(response), NextAvailableAt: next}
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
