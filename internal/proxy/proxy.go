package proxy

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/neverknowerdev/paylessforai/internal/catalog"
	"github.com/neverknowerdev/paylessforai/internal/ids"
	"github.com/neverknowerdev/paylessforai/internal/matcher"
	"github.com/neverknowerdev/paylessforai/internal/providers"
	"github.com/neverknowerdev/paylessforai/internal/retry"
	"github.com/neverknowerdev/paylessforai/internal/store"
	"github.com/neverknowerdev/paylessforai/internal/usage"
)

const defaultMaximumBody = 32 << 20

type Proxy struct {
	Catalog          *catalog.Manager
	Store            *store.Store
	Retry            retry.Engine
	MaximumBody      int64
	RequireClientKey bool
}

func New(catalogManager *catalog.Manager, dataStore *store.Store) *Proxy {
	return &Proxy{Catalog: catalogManager, Store: dataStore, Retry: retry.New(), MaximumBody: defaultMaximumBody, RequireClientKey: true}
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request, protocol matcher.Protocol) {
	requestID := ids.New()
	w.Header().Set("X-PayLess-Request-ID", requestID)
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "inference endpoint only accepts POST")
		return
	}
	clientKeyID := ""
	if p.RequireClientKey {
		secret := bearerToken(r.Header.Get("Authorization"))
		if secret == "" {
			secret = strings.TrimSpace(r.Header.Get("x-api-key"))
		}
		if secret == "" || p.Store == nil {
			writeError(w, http.StatusUnauthorized, "invalid_api_key", "a PayLessForAI client API key is required")
			return
		}
		key, ok, err := p.Store.AuthenticateClientKey(r.Context(), secret)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "key_lookup_failed", "client key lookup failed")
			return
		}
		if !ok {
			writeError(w, http.StatusUnauthorized, "invalid_api_key", "the client API key is invalid or revoked")
			return
		}
		clientKeyID = key.ID
	}
	body, err := readBody(r, p.MaximumBody)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	request, err := parseRequest(body, protocol)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if p.Store != nil {
		_ = p.Store.CreateProxyRequest(r.Context(), requestID, clientKeyID, string(protocol), request.Model)
	}
	if p.Catalog == nil {
		p.finishError(r.Context(), requestID, "not_configured", "provider catalog is not configured")
		writeError(w, http.StatusServiceUnavailable, "not_configured", "provider catalog is not configured")
		return
	}
	snapshot := p.Catalog.Snapshot()
	match := matcher.New().Match(matcher.MatchInput{Request: request.MatchRequest(protocol), Routes: snapshot.Routes, Now: time.Now().UTC()})
	if match.Selected == nil {
		message := "no compatible provider route is available"
		if match.Error != nil {
			message = match.Error.Message
		}
		p.finishError(r.Context(), requestID, "no_eligible_route", message)
		writeError(w, http.StatusServiceUnavailable, "no_eligible_route", message)
		return
	}
	if err := p.execute(r.Context(), w, requestID, body, request, match.Ranked); err != nil {
		var partial *partialStreamError
		if errors.As(err, &partial) {
			return
		}
		p.finishError(r.Context(), requestID, errorCode(err), sanitize(err.Error()))
		writeError(w, statusFor(err), errorCode(err), sanitize(err.Error()))
	}
}

type parsedRequest struct {
	Protocol          matcher.Protocol
	Model             string
	InputTokens       int64
	ExpectedOutput    int64
	MaxContext        int64
	MaxOutput         int64
	RequiredParams    []string
	RequireTools      bool
	RequireStructured bool
	Stream            bool
}

func (p parsedRequest) MatchRequest(protocol matcher.Protocol) matcher.MatchRequest {
	return matcher.MatchRequest{Protocol: protocol, LogicalModel: p.Model, RequiredParameters: p.RequiredParams, RequireTools: p.RequireTools, RequireStructured: p.RequireStructured, InputTokens: p.InputTokens, ExpectedOutput: p.ExpectedOutput, MaxContext: p.MaxContext, MaxOutput: p.MaxOutput}
}

func parseRequest(body []byte, protocol matcher.Protocol) (parsedRequest, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return parsedRequest{}, fmt.Errorf("body must be valid JSON: %w", err)
	}
	request := parsedRequest{Protocol: protocol}
	if err := json.Unmarshal(payload["model"], &request.Model); err != nil || strings.TrimSpace(request.Model) == "" {
		return parsedRequest{}, errors.New("model is required")
	}
	if protocol == matcher.ProtocolChatCompletions || protocol == matcher.ProtocolAnthropic {
		if _, ok := payload["messages"]; !ok {
			return parsedRequest{}, errors.New("messages is required")
		}
	}
	request.Stream = boolValue(payload["stream"])
	request.ExpectedOutput = integerValue(payload, "max_tokens", "max_completion_tokens", "max_output_tokens")
	if request.ExpectedOutput == 0 {
		request.ExpectedOutput = 256
	}
	request.InputTokens = int64(len(body) / 4)
	if request.InputTokens == 0 {
		request.InputTokens = 1
	}
	if _, ok := payload["tools"]; ok {
		request.RequireTools = true
		request.RequiredParams = append(request.RequiredParams, "tools")
	}
	if _, ok := payload["response_format"]; ok {
		request.RequireStructured = true
		request.RequiredParams = append(request.RequiredParams, "response_format")
	}
	return request, nil
}

func (p *Proxy) execute(ctx context.Context, writer http.ResponseWriter, requestID string, body []byte, request parsedRequest, ranked []matcher.RankedRoute) error {
	current := 0
	policy := retry.DefaultPolicy()
	for attempt := 1; attempt <= policy.MaximumAttempts; attempt++ {
		if current >= len(ranked) {
			return &proxyError{status: http.StatusServiceUnavailable, code: "no_fallback_route", message: "all eligible routes were exhausted"}
		}
		route := ranked[current].Route
		client := p.Catalog.Client(route.Provider)
		if client == nil {
			return &proxyError{status: http.StatusBadGateway, code: "provider_not_configured", message: "selected provider is not configured"}
		}
		response, err := client.Do(ctx, request.Protocol, route.UpstreamModel, body)
		if err == nil {
			if request.Stream || strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream") {
				return p.stream(ctx, writer, requestID, response, ranked[current].ExpectedCost, route.Price)
			}
			return p.complete(ctx, writer, requestID, response, ranked[current].ExpectedCost, route.Price)
		}
		classified := classify(err)
		decision := p.Retry.Decide(retry.Input{Policy: policy, AttemptNumber: attempt, Now: time.Now(), Error: classified, Delivery: retry.NothingSent, SameRouteAvailable: !route.Free, FallbacksRemaining: len(ranked) - current - 1})
		if decision.Action != retry.RetrySameRoute && decision.Action != retry.FailOver {
			return err
		}
		if decision.Action == retry.FailOver {
			current++
		}
		if err := wait(ctx, decision.Delay); err != nil {
			return err
		}
	}
	return &proxyError{status: http.StatusBadGateway, code: "attempt_budget_exhausted", message: "provider attempt budget exhausted"}
}

func (p *Proxy) complete(ctx context.Context, writer http.ResponseWriter, requestID string, response *http.Response, expectedCost int64, price matcher.Price) error {
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, p.MaximumBody))
	if err != nil {
		return err
	}
	copyHeaders(writer.Header(), response.Header)
	writer.WriteHeader(response.StatusCode)
	_, _ = writer.Write(body)
	persistUsage(ctx, p.Store, requestID, usage.FromJSON(body), expectedCost, price)
	if p.Store != nil {
		_ = p.Store.CompleteProxyRequest(ctx, requestID, "succeeded", "", "")
	}
	return nil
}

func (p *Proxy) stream(ctx context.Context, writer http.ResponseWriter, requestID string, response *http.Response, expectedCost int64, price matcher.Price) error {
	defer response.Body.Close()
	copyHeaders(writer.Header(), response.Header)
	writer.WriteHeader(response.StatusCode)
	flusher, _ := writer.(http.Flusher)
	reader := bufio.NewReaderSize(response.Body, 64<<10)
	stats := usage.Stats{}
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			if _, writeErr := writer.Write(line); writeErr != nil {
				return writeErr
			}
			if flusher != nil {
				flusher.Flush()
			}
			observeSSE(line, &stats)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				persistUsage(ctx, p.Store, requestID, stats, expectedCost, price)
				if p.Store != nil {
					_ = p.Store.CompleteProxyRequest(ctx, requestID, "succeeded", "", "")
				}
				return nil
			}
			if p.Store != nil {
				_ = p.Store.CompleteProxyRequest(ctx, requestID, "partial", "stream_error", sanitize(err.Error()))
			}
			return &partialStreamError{err: err}
		}
	}
}

func observeSSE(line []byte, stats *usage.Stats) {
	trimmed := strings.TrimSpace(string(line))
	if !strings.HasPrefix(trimmed, "data:") {
		return
	}
	data := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
	if data == "" || data == "[DONE]" {
		return
	}
	var envelope map[string]any
	if json.Unmarshal([]byte(data), &envelope) != nil {
		return
	}
	observed := usage.FromEnvelope(envelope)
	if observed.InputTokens != 0 {
		stats.InputTokens = observed.InputTokens
	}
	if observed.OutputTokens != 0 {
		stats.OutputTokens = observed.OutputTokens
	}
	if observed.TotalTokens != 0 {
		stats.TotalTokens = observed.TotalTokens
	}
	if observed.CachedReadTokens != 0 {
		stats.CachedReadTokens = observed.CachedReadTokens
	}
	if observed.CacheWriteTokens != 0 {
		stats.CacheWriteTokens = observed.CacheWriteTokens
	}
	if observed.ReasoningTokens != 0 {
		stats.ReasoningTokens = observed.ReasoningTokens
	}
	if observed.ActualCostPicoUSD != nil {
		stats.ActualCostPicoUSD = observed.ActualCostPicoUSD
	}
	stats.Raw = observed.Raw
}

func persistUsage(ctx context.Context, dataStore *store.Store, requestID string, stats usage.Stats, expectedCost int64, price matcher.Price) {
	if dataStore == nil {
		return
	}
	actualCost := stats.ActualCostPicoUSD
	if actualCost == nil && (stats.InputTokens > 0 || stats.OutputTokens > 0 || stats.CachedReadTokens > 0 || stats.CacheWriteTokens > 0 || stats.ReasoningTokens > 0) {
		if calculated, err := matcher.EstimateUsageCost(matcher.UsageCostInput{InputTokens: stats.InputTokens, OutputTokens: stats.OutputTokens, CachedReadTokens: stats.CachedReadTokens, CacheWriteTokens: stats.CacheWriteTokens, ReasoningTokens: stats.ReasoningTokens, InputTokensNetOfCache: stats.InputTokensNetOfCache}, price); err == nil {
			actualCost = &calculated
		}
	}
	raw, _ := json.Marshal(stats.Raw)
	_ = dataStore.RecordUsage(ctx, store.RequestUsage{RequestID: requestID, InputTokens: stats.InputTokens, OutputTokens: stats.OutputTokens, TotalTokens: stats.TotalTokens, CachedReadTokens: stats.CachedReadTokens, CacheWriteTokens: stats.CacheWriteTokens, ReasoningTokens: stats.ReasoningTokens, EstimatedCostPico: expectedCost, ActualCostPico: actualCost, RawUsageJSON: string(raw)})
}

func classify(err error) retry.ClassifiedError {
	var upstream *providers.UpstreamError
	if errors.As(err, &upstream) {
		var delay *time.Duration
		if upstream.RetryAfter != nil {
			value := time.Duration(*upstream.RetryAfter) * time.Second
			delay = &value
		}
		return retry.ClassifiedError{Class: upstream.Class, HTTPStatus: upstream.StatusCode, RetryAfter: delay, Description: upstream.Message}
	}
	return retry.ClassifiedError{Class: retry.ErrorTransport, Description: err.Error()}
}

func wait(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func readBody(request *http.Request, maximum int64) ([]byte, error) {
	if maximum <= 0 {
		maximum = defaultMaximumBody
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maximum {
		return nil, errors.New("request body is too large")
	}
	return body, nil
}

func bearerToken(value string) string {
	parts := strings.Fields(value)
	if len(parts) == 2 && strings.EqualFold(parts[0], "bearer") {
		return parts[1]
	}
	return ""
}

func boolValue(raw json.RawMessage) bool {
	var value bool
	_ = json.Unmarshal(raw, &value)
	return value
}

func integerValue(values map[string]json.RawMessage, names ...string) int64 {
	for _, name := range names {
		var value int64
		if json.Unmarshal(values[name], &value) == nil && value > 0 {
			return value
		}
	}
	return 0
}

func copyHeaders(destination, source http.Header) {
	for key, values := range source {
		if strings.EqualFold(key, "Content-Length") || strings.EqualFold(key, "Transfer-Encoding") || strings.EqualFold(key, "Connection") {
			continue
		}
		for _, value := range values {
			destination.Add(key, value)
		}
	}
}

type proxyError struct {
	status  int
	code    string
	message string
}

type partialStreamError struct{ err error }

func (e *partialStreamError) Error() string { return e.err.Error() }

func (e *partialStreamError) Unwrap() error { return e.err }

func (e *proxyError) Error() string { return e.message }

func statusFor(err error) int {
	var proxyErr *proxyError
	if errors.As(err, &proxyErr) && proxyErr.status != 0 {
		return proxyErr.status
	}
	var upstreamErr *providers.UpstreamError
	if errors.As(err, &upstreamErr) && upstreamErr.StatusCode >= 400 && upstreamErr.StatusCode <= 599 {
		return upstreamErr.StatusCode
	}
	return http.StatusBadGateway
}

func errorCode(err error) string {
	var proxyErr *proxyError
	if errors.As(err, &proxyErr) && proxyErr.code != "" {
		return proxyErr.code
	}
	var upstreamErr *providers.UpstreamError
	if errors.As(err, &upstreamErr) {
		switch upstreamErr.Class {
		case retry.ErrorInvalidRequest:
			return "invalid_request"
		case retry.ErrorAuthentication:
			return "provider_authentication"
		case retry.ErrorPayment:
			return "provider_payment_required"
		case retry.ErrorModelNotFound:
			return "model_not_found"
		case retry.ErrorRateLimit:
			return "provider_rate_limit"
		case retry.ErrorTimeout:
			return "provider_timeout"
		}
	}
	return "upstream_error"
}

func (p *Proxy) finishError(ctx context.Context, requestID, code, message string) {
	if p.Store != nil {
		_ = p.Store.CompleteProxyRequest(ctx, requestID, "failed", code, message)
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"type": "payless_error", "code": code, "message": message}})
}

func sanitize(message string) string {
	message = strings.Join(strings.Fields(message), " ")
	if len(message) > 1024 {
		return message[:1024]
	}
	return message
}
