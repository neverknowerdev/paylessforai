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
	"github.com/neverknowerdev/paylessforai/internal/db/models"
	"github.com/neverknowerdev/paylessforai/internal/db/repositories"
	"github.com/neverknowerdev/paylessforai/internal/groups"
	"github.com/neverknowerdev/paylessforai/internal/ids"
	"github.com/neverknowerdev/paylessforai/internal/matcher"
	"github.com/neverknowerdev/paylessforai/internal/providers"
	"github.com/neverknowerdev/paylessforai/internal/retry"
	"github.com/neverknowerdev/paylessforai/internal/routing"
	"github.com/neverknowerdev/paylessforai/internal/usage"
)

const defaultMaximumBody = 32 << 20

type Proxy struct {
	Catalog          *catalog.Manager
	Repositories     *repositories.Repositories
	Retry            retry.Engine
	MaximumBody      int64
	RequireClientKey bool
	Groups           *groups.Manager
}

func recordResolution(ctx context.Context, repos *repositories.Repositories, requestID string, plan routing.Plan) error {
	data, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	selected := ""
	if entry := plan.Selected(); entry != nil {
		selected = entry.Route.LogicalModel
	}
	return repos.ProxyRequests.RecordResolution(ctx, requestID, plan.GroupID, plan.GroupRevision, string(data), selected)
}

func recordProxyAttemptRoute(ctx context.Context, repos *repositories.Repositories, requestID string, attempt int, routeID, credentialID, stageID, stagePath, provider, upstream, state, errorClass, errorMessage string, rawError ...string) error {
	if err := repos.ProxyRequests.RecordAttemptRoute(ctx, requestID, attempt, provider, upstream); err != nil {
		return err
	}
	if err := repos.ProxyAttempts.Record(ctx, requestID, attempt, provider, upstream, state, errorClass, errorMessage, rawError...); err != nil {
		return err
	}
	return repos.ProxyAttempts.UpdateRoute(ctx, requestID, attempt, routeID, credentialID, stageID, stagePath)
}

func New(catalogManager *catalog.Manager, repos *repositories.Repositories) *Proxy {
	return &Proxy{Catalog: catalogManager, Repositories: repos, Retry: retry.New(), MaximumBody: defaultMaximumBody, RequireClientKey: true}
}

func (p *Proxy) SetGroups(manager *groups.Manager) { p.Groups = manager }

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
		if secret == "" || p.Repositories == nil {
			writeError(w, http.StatusUnauthorized, "invalid_api_key", "a PayLessForAI client API key is required")
			return
		}
		key, ok, err := p.Repositories.ClientAPIKeys.Authenticate(r.Context(), secret)
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
	if p.Repositories != nil {
		_ = p.Repositories.ProxyRequests.Create(r.Context(), requestID, clientKeyID, string(protocol), request.Model)
	}
	if p.Catalog == nil {
		p.finishError(r.Context(), requestID, "not_configured", "provider catalog is not configured")
		writeError(w, http.StatusServiceUnavailable, "not_configured", "provider catalog is not configured")
		return
	}
	snapshot := p.Catalog.Snapshot()
	plan := routing.BuildDirect(request.MatchRequest(protocol), snapshot.Routes, time.Now().UTC())
	if p.Groups != nil {
		if definition, ok := p.Groups.FindBySlug(request.Model); ok {
			plan = routing.BuildGroup(request.MatchRequest(protocol), definition, p.Groups.DefinitionsByID(), snapshot.Routes, time.Now().UTC(), routing.DefaultLimits())
		}
	}
	if plan.Selected() == nil {
		message := "no compatible provider route is available"
		code := "no_eligible_route"
		if plan.Error != nil {
			message, code = plan.Error.Message, plan.Error.Code
		}
		p.finishError(r.Context(), requestID, code, message)
		status := http.StatusServiceUnavailable
		if code == "group_price_limit_exceeded" {
			status = http.StatusUnprocessableEntity
		}
		writeError(w, status, code, message)
		return
	}
	if p.Repositories != nil {
		_ = recordResolution(r.Context(), p.Repositories, requestID, plan)
	}
	officialPrice, officialExpectedCost := officialPricing(planRanked(plan))
	if err := p.execute(r.Context(), w, requestID, body, request, plan, officialPrice, officialExpectedCost); err != nil {
		var partial *partialStreamError
		if errors.As(err, &partial) {
			return
		}
		p.finishError(r.Context(), requestID, errorCode(err), sanitize(err.Error()))
		writeError(w, statusFor(err), errorCode(err), sanitize(err.Error()))
	}
}

type parsedRequest struct {
	Protocol                 matcher.Protocol
	Model                    string
	InputTokens              int64
	ExpectedOutput           int64
	MaxContext               int64
	MaxOutput                int64
	RequiredParams           []string
	RequireTools             bool
	RequireStructured        bool
	Stream                   bool
	RequiredInputModalities  []string
	RequiredOutputModalities []string
}

func (p parsedRequest) MatchRequest(protocol matcher.Protocol) matcher.MatchRequest {
	return matcher.MatchRequest{Protocol: protocol, LogicalModel: p.Model, RequiredParameters: p.RequiredParams, RequireTools: p.RequireTools, RequireStructured: p.RequireStructured, InputTokens: p.InputTokens, ExpectedOutput: p.ExpectedOutput, MaxContext: p.MaxContext, MaxOutput: p.MaxOutput, RequiredInputModalities: p.RequiredInputModalities, RequiredOutputModalities: p.RequiredOutputModalities}
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
	var decoded any
	if raw, ok := payload["messages"]; ok {
		_ = json.Unmarshal(raw, &decoded)
		request.RequiredInputModalities = detectModalities(decoded)
	}
	if raw, ok := payload["input"]; ok {
		_ = json.Unmarshal(raw, &decoded)
		request.RequiredInputModalities = appendUnique(request.RequiredInputModalities, detectModalities(decoded)...)
	}
	if raw, ok := payload["modalities"]; ok {
		var output []string
		if json.Unmarshal(raw, &output) == nil {
			request.RequiredOutputModalities = normalizeModalities(output)
		}
	}
	return request, nil
}

func detectModalities(value any) []string {
	result := []string{}
	var visit func(any)
	visit = func(node any) {
		switch item := node.(type) {
		case []any:
			for _, child := range item {
				visit(child)
			}
		case map[string]any:
			if typ, ok := item["type"].(string); ok {
				switch strings.ToLower(typ) {
				case "text", "input_text", "output_text":
					result = appendUnique(result, "text")
				case "image", "image_url", "input_image":
					result = appendUnique(result, "image")
				case "audio", "input_audio":
					result = appendUnique(result, "audio")
				case "video", "input_video":
					result = appendUnique(result, "video")
				}
			}
			for key, child := range item {
				if key == "content" || key == "input" || key == "parts" {
					visit(child)
				}
			}
		case string:
			if strings.TrimSpace(item) != "" {
				result = appendUnique(result, "text")
			}
		}
	}
	visit(value)
	return result
}

func normalizeModalities(values []string) []string {
	result := []string{}
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" {
			result = appendUnique(result, value)
		}
	}
	return result
}
func appendUnique(values []string, additions ...string) []string {
	seen := map[string]bool{}
	for _, value := range values {
		seen[value] = true
	}
	for _, value := range additions {
		if !seen[value] {
			seen[value] = true
			values = append(values, value)
		}
	}
	return values
}

func planRanked(plan routing.Plan) []matcher.RankedRoute {
	result := make([]matcher.RankedRoute, 0, len(plan.Entries))
	for _, entry := range plan.Entries {
		result = append(result, matcher.RankedRoute{Route: entry.Route, ExpectedCost: entry.ExpectedCost})
	}
	return result
}

func (p *Proxy) execute(ctx context.Context, writer http.ResponseWriter, requestID string, body []byte, request parsedRequest, plan routing.Plan, officialPrice matcher.Price, officialExpectedCost int64) error {
	current := 0
	blocked := false
	policy := retry.DefaultPolicy()
	policy.MaximumAttempts = routing.DefaultLimits().MaximumAttempts
	totalAttempts := 0
	retriesRemaining := -1
	for totalAttempts < policy.MaximumAttempts {
		if current >= len(plan.Entries) {
			if blocked {
				return &proxyError{status: http.StatusTooManyRequests, code: "all_subscription_quotas_exhausted", message: "all eligible subscription provider accounts are temporarily limited"}
			}
			return &proxyError{status: http.StatusServiceUnavailable, code: "no_fallback_route", message: "all eligible routes were exhausted"}
		}
		entry := plan.Entries[current]
		route := entry.Route
		if retriesRemaining < 0 {
			retriesRemaining = entry.SameRouteRetries
		}
		blockKey := route.ExecutionKey
		if blockKey == "" {
			blockKey = route.Provider
		}
		if p.Catalog.ProviderBlocked(blockKey, time.Now().UTC()) {
			blocked = true
			current++
			retriesRemaining = -1
			continue
		}
		client := p.Catalog.ClientForRoute(route)
		if client == nil {
			if p.Repositories != nil {
				_ = recordProxyAttemptRoute(ctx, p.Repositories, requestID, totalAttempts+1, route.ID, route.CredentialID, entry.StageID, strings.Join(entry.StagePath, " / "), route.Provider, route.UpstreamModel, "failed", "provider_not_configured", "Selected provider is not configured.", "selected provider is not configured")
			}
			return &proxyError{status: http.StatusBadGateway, code: "provider_not_configured", message: "selected provider is not configured"}
		}
		totalAttempts++
		if p.Repositories != nil {
			_ = recordProxyAttemptRoute(ctx, p.Repositories, requestID, totalAttempts, route.ID, route.CredentialID, entry.StageID, strings.Join(entry.StagePath, " / "), route.Provider, route.UpstreamModel, "started", "", "")
		}
		response, err := client.Do(ctx, request.Protocol, route.UpstreamModel, body)
		if err == nil {
			if request.Stream || strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream") {
				streamErr := p.stream(ctx, writer, requestID, response, entry.ExpectedCost, officialExpectedCost, route.Price, officialPrice)
				if p.Repositories != nil {
					state, code, message := "succeeded", "", ""
					raw := ""
					if streamErr != nil {
						state, code, message, raw = "partial", "stream_error", humanErrorMessage(streamErr), sanitize(streamErr.Error())
					}
					_ = recordProxyAttemptRoute(ctx, p.Repositories, requestID, totalAttempts, route.ID, route.CredentialID, entry.StageID, strings.Join(entry.StagePath, " / "), route.Provider, route.UpstreamModel, state, code, message, raw)
				}
				return streamErr
			}
			completeErr := p.complete(ctx, writer, requestID, response, entry.ExpectedCost, officialExpectedCost, route.Price, officialPrice)
			if p.Repositories != nil {
				state, code, message := "succeeded", "", ""
				raw := ""
				if completeErr != nil {
					state, code, message, raw = "failed", errorCode(completeErr), humanErrorMessage(completeErr), sanitize(completeErr.Error())
				}
				_ = recordProxyAttemptRoute(ctx, p.Repositories, requestID, totalAttempts, route.ID, route.CredentialID, entry.StageID, strings.Join(entry.StagePath, " / "), route.Provider, route.UpstreamModel, state, code, message, raw)
			}
			return completeErr
		}
		classified := classify(err)
		if classified.Class == retry.ErrorQuotaExhausted {
			blocked = true
			var upstream *providers.UpstreamError
			if errors.As(err, &upstream) {
				p.Catalog.SetProviderBlocked(blockKey, upstream.NextAvailableAt)
				if p.Repositories != nil {
					if route.CredentialID != "" {
						_ = p.Repositories.ProviderCredentials.MarkLimitedByID(ctx, route.CredentialID, upstream.NextAvailableAt, upstream.Message)
					} else {
						_ = p.Repositories.ProviderCredentials.MarkLimited(ctx, route.Provider, upstream.NextAvailableAt, upstream.Message)
					}
				}
			}
		}
		if p.Repositories != nil {
			_ = recordProxyAttemptRoute(ctx, p.Repositories, requestID, totalAttempts, route.ID, route.CredentialID, entry.StageID, strings.Join(entry.StagePath, " / "), route.Provider, route.UpstreamModel, "failed", errorCode(err), humanErrorMessage(err), rawErrorMessage(err))
		}
		decision := p.Retry.Decide(retry.Input{Policy: policy, AttemptNumber: totalAttempts, Now: time.Now(), Error: classified, Delivery: retry.NothingSent, SameRouteAvailable: !route.Free, FallbacksRemaining: len(plan.Entries) - current - 1, PlanMode: true, SameRouteRetriesRemaining: retriesRemaining, PlanEntriesRemaining: len(plan.Entries) - current - 1, TotalAttemptsRemaining: policy.MaximumAttempts - totalAttempts})
		if decision.Action != retry.RetrySameRoute && decision.Action != retry.FailOver {
			return err
		}
		if decision.Action == retry.FailOver {
			current++
			retriesRemaining = -1
		} else if retriesRemaining > 0 {
			retriesRemaining--
		}
		if err := wait(ctx, decision.Delay); err != nil {
			return err
		}
	}
	return &proxyError{status: http.StatusBadGateway, code: "attempt_budget_exhausted", message: "provider attempt budget exhausted"}
}

func (p *Proxy) complete(ctx context.Context, writer http.ResponseWriter, requestID string, response *http.Response, expectedCost, officialExpectedCost int64, price, officialPrice matcher.Price) error {
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, p.MaximumBody))
	if err != nil {
		return err
	}
	copyHeaders(writer.Header(), response.Header)
	writer.WriteHeader(response.StatusCode)
	_, _ = writer.Write(body)
	persistUsage(ctx, p.Repositories, requestID, usage.FromJSON(body), expectedCost, officialExpectedCost, price, officialPrice)
	if p.Repositories != nil {
		_ = p.Repositories.ProxyRequests.Complete(ctx, requestID, "succeeded", "", "")
	}
	return nil
}

func (p *Proxy) stream(ctx context.Context, writer http.ResponseWriter, requestID string, response *http.Response, expectedCost, officialExpectedCost int64, price, officialPrice matcher.Price) error {
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
				persistUsage(ctx, p.Repositories, requestID, stats, expectedCost, officialExpectedCost, price, officialPrice)
				if p.Repositories != nil {
					_ = p.Repositories.ProxyRequests.Complete(ctx, requestID, "succeeded", "", "")
				}
				return nil
			}
			if p.Repositories != nil {
				_ = p.Repositories.ProxyRequests.Complete(ctx, requestID, "partial", "stream_error", sanitize(err.Error()))
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

func persistUsage(ctx context.Context, repos *repositories.Repositories, requestID string, stats usage.Stats, expectedCost, officialExpectedCost int64, price, officialPrice matcher.Price) {
	if repos == nil {
		return
	}
	actualCost := stats.ActualCostPicoUSD
	if actualCost == nil && (stats.InputTokens > 0 || stats.OutputTokens > 0 || stats.CachedReadTokens > 0 || stats.CacheWriteTokens > 0 || stats.ReasoningTokens > 0) {
		if calculated, err := matcher.EstimateUsageCost(matcher.UsageCostInput{InputTokens: stats.InputTokens, OutputTokens: stats.OutputTokens, CachedReadTokens: stats.CachedReadTokens, CacheWriteTokens: stats.CacheWriteTokens, ReasoningTokens: stats.ReasoningTokens, InputTokensNetOfCache: stats.InputTokensNetOfCache}, price); err == nil {
			actualCost = &calculated
		}
	}
	officialCost := officialExpectedCost
	if stats.InputTokens > 0 || stats.OutputTokens > 0 || stats.CachedReadTokens > 0 || stats.CacheWriteTokens > 0 || stats.ReasoningTokens > 0 {
		if calculated, err := matcher.EstimateUsageCost(matcher.UsageCostInput{InputTokens: stats.InputTokens, OutputTokens: stats.OutputTokens, CachedReadTokens: stats.CachedReadTokens, CacheWriteTokens: stats.CacheWriteTokens, ReasoningTokens: stats.ReasoningTokens, InputTokensNetOfCache: stats.InputTokensNetOfCache}, officialPrice); err == nil {
			officialCost = calculated
		}
	}
	var discountPico, discountBPS *int64
	if actualCost != nil && officialCost > 0 {
		difference := officialCost - *actualCost
		if difference < 0 {
			difference = 0
		}
		maxInt64 := int64(^uint64(0) >> 1)
		bps := int64(0)
		if difference > maxInt64/10000 {
			bps = int64(float64(difference) / float64(officialCost) * 10000)
		} else {
			bps = difference * 10000 / officialCost
		}
		if bps > 10000 {
			bps = 10000
		}
		discountPico, discountBPS = &difference, &bps
	}
	raw, _ := json.Marshal(stats.Raw)
	_ = repos.RequestUsage.Upsert(ctx, models.RequestUsage{RequestID: requestID, InputTokens: stats.InputTokens, OutputTokens: stats.OutputTokens, TotalTokens: stats.TotalTokens, CachedReadTokens: stats.CachedReadTokens, CacheWriteTokens: stats.CacheWriteTokens, ReasoningTokens: stats.ReasoningTokens, EstimatedCostPico: expectedCost, OfficialCostPico: officialCost, ActualCostPico: actualCost, DiscountPico: discountPico, DiscountBPS: discountBPS, RawUsageJSON: string(raw)})
}

func officialPricing(ranked []matcher.RankedRoute) (matcher.Price, int64) {
	for _, candidate := range ranked {
		if candidate.Route.Provider == "openrouter" && !candidate.Route.Free {
			return referencePrice(candidate.Route), candidate.ExpectedCost
		}
	}
	for _, candidate := range ranked {
		if !candidate.Route.Free {
			return referencePrice(candidate.Route), candidate.ExpectedCost
		}
	}
	if len(ranked) > 0 {
		return referencePrice(ranked[0].Route), ranked[0].ExpectedCost
	}
	return matcher.Price{}, 0
}

func referencePrice(route matcher.Route) matcher.Price {
	if route.OfficialPriceAvailable {
		return route.OfficialPrice
	}
	return route.Price
}

func humanErrorMessage(err error) string {
	var upstream *providers.UpstreamError
	if errors.As(err, &upstream) {
		if message := parseProviderError(upstream.Message); message != "" {
			return message
		}
		return sanitize(upstream.Message)
	}
	return sanitize(err.Error())
}

func rawErrorMessage(err error) string {
	var upstream *providers.UpstreamError
	if errors.As(err, &upstream) {
		return sanitize(upstream.Message)
	}
	return sanitize(err.Error())
}

func parseProviderError(raw string) string {
	var value any
	if json.Unmarshal([]byte(raw), &value) != nil {
		return strings.TrimSpace(raw)
	}
	var visit func(any) string
	visit = func(node any) string {
		object, ok := node.(map[string]any)
		if !ok {
			return ""
		}
		if metadata, ok := object["metadata"].(map[string]any); ok {
			if detail, ok := metadata["raw"].(string); ok && strings.TrimSpace(detail) != "" {
				return strings.TrimSpace(detail)
			}
		}
		if nested, ok := object["error"]; ok {
			if detail := visit(nested); detail != "" {
				return detail
			}
		}
		if message, ok := object["message"].(string); ok && strings.TrimSpace(message) != "" {
			return strings.TrimSpace(message)
		}
		return ""
	}
	if message := visit(value); message != "" {
		return message
	}
	return strings.TrimSpace(raw)
}

func classify(err error) retry.ClassifiedError {
	var upstream *providers.UpstreamError
	if errors.As(err, &upstream) {
		var delay *time.Duration
		if upstream.RetryAfter != nil {
			value := time.Duration(*upstream.RetryAfter) * time.Second
			delay = &value
		}
		return retry.ClassifiedError{Class: upstream.Class, HTTPStatus: upstream.StatusCode, RetryAfter: delay, NextAvailableAt: upstream.NextAvailableAt, Description: upstream.Message}
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
		case retry.ErrorQuotaExhausted:
			return "provider_quota_exhausted"
		case retry.ErrorTimeout:
			return "provider_timeout"
		}
	}
	return "upstream_error"
}

func (p *Proxy) finishError(ctx context.Context, requestID, code, message string) {
	if p.Repositories != nil {
		_ = p.Repositories.ProxyRequests.Complete(ctx, requestID, "failed", code, message)
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
