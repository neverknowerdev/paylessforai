package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/neverknowerdev/paylessforai/internal/catalog"
	"github.com/neverknowerdev/paylessforai/internal/clientauth"
	"github.com/neverknowerdev/paylessforai/internal/db/models"
	"github.com/neverknowerdev/paylessforai/internal/db/repositories"
	"github.com/neverknowerdev/paylessforai/internal/groups"
	"github.com/neverknowerdev/paylessforai/internal/ids"
	"github.com/neverknowerdev/paylessforai/internal/matcher"
	"github.com/neverknowerdev/paylessforai/internal/providers"
	"github.com/neverknowerdev/paylessforai/internal/retry"
	"github.com/neverknowerdev/paylessforai/internal/routing"
	"github.com/neverknowerdev/paylessforai/internal/session"
	"github.com/neverknowerdev/paylessforai/internal/usage"
	"github.com/neverknowerdev/paylessforai/internal/wire"
)

const defaultMaximumBody = 32 << 20

type Proxy struct {
	Catalog          *catalog.Manager
	Repositories     *repositories.Repositories
	Retry            retry.Engine
	MaximumBody      int64
	RequireClientKey bool
	Groups           *groups.Manager
	formatMu         sync.Mutex
	formatLocks      map[string]*sync.Mutex
	SessionDetector  *session.Detector
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

func recordProxyAttemptRoute(ctx context.Context, repos *repositories.Repositories, requestID string, attempt int, routeID, credentialID, stageID, stagePath, provider, upstream, state, errorClass, errorMessage string, httpStatus *int, rawError ...string) error {
	return recordProxyAttemptRouteFormats(ctx, repos, requestID, attempt, routeID, credentialID, stageID, stagePath, provider, upstream, state, errorClass, errorMessage, wire.FormatUnknown, wire.FormatUnknown, httpStatus, rawError...)
}

func recordProxyAttemptRouteFormats(ctx context.Context, repos *repositories.Repositories, requestID string, attempt int, routeID, credentialID, stageID, stagePath, provider, upstream, state, errorClass, errorMessage string, clientFormat, providerFormat wire.Format, httpStatus *int, rawError ...string) error {
	if err := repos.ProxyRequests.RecordAttemptRoute(ctx, requestID, attempt, provider, upstream); err != nil {
		return err
	}
	if err := repos.ProxyAttempts.RecordWithHTTPStatus(ctx, requestID, attempt, provider, upstream, state, errorClass, errorMessage, clientFormat, providerFormat, httpStatus, rawError...); err != nil {
		return err
	}
	return repos.ProxyAttempts.UpdateRoute(ctx, requestID, attempt, routeID, credentialID, stageID, stagePath)
}

func New(catalogManager *catalog.Manager, repos *repositories.Repositories) *Proxy {
	var store session.Store
	if repos != nil {
		store = repos.Sessions
	}
	return &Proxy{Catalog: catalogManager, Repositories: repos, Retry: retry.New(), MaximumBody: defaultMaximumBody, RequireClientKey: true, formatLocks: make(map[string]*sync.Mutex), SessionDetector: session.NewDetector(store, nil)}
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
		if id, ok := clientauth.KeyID(r.Context()); ok {
			clientKeyID = id
		} else {
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
	}
	body, err := readBody(r, p.MaximumBody)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_body", err.Error())
		return
	}
	clientFormat := wire.FormatForProtocol(string(protocol))
	canonical, err := wire.DecodeRequest(clientFormat, body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	request, err := parseRequest(body, protocol, canonical)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	sessionID := ""
	if p.SessionDetector != nil {
		sessionID = p.SessionDetector.DetectSessionID(r.Context(), session.Input{ClientKeyID: clientKeyID, Format: clientFormat, Headers: r.Header, Body: body})
	}
	if p.Repositories != nil {
		_ = p.Repositories.ProxyRequests.Create(r.Context(), requestID, clientKeyID, string(protocol), request.Model, sessionID)
	}
	if p.Catalog == nil {
		p.finishError(r.Context(), requestID, "not_configured", "provider catalog is not configured")
		writeError(w, http.StatusServiceUnavailable, "not_configured", "provider catalog is not configured")
		return
	}
	snapshot := p.Catalog.Snapshot()
	matchRequest := request.MatchRequest(protocol)
	if request.RequireStructured {
		// Structured capability is resolved lazily for the exact route below.
		// Keep unknown routes in the initial ranking so a request can probe only
		// the candidates it actually needs instead of the whole catalog.
		matchRequest.RequireStructured = false
		matchRequest.RequiredParameters = withoutStructuredOutputParameter(matchRequest.RequiredParameters)
	}
	plan := routing.BuildDirect(matchRequest, snapshot.Routes, time.Now().UTC())
	if p.Groups != nil {
		if definition, ok := p.Groups.FindBySlug(request.Model); ok {
			plan = routing.BuildGroup(matchRequest, definition, p.Groups.DefinitionsByID(), snapshot.Routes, time.Now().UTC(), routing.DefaultLimits())
		}
	}
	if request.RequireStructured {
		plan = p.resolveStructuredPlan(r.Context(), plan)
	}
	if p.Repositories != nil {
		// Persist the complete plan even when every route was rejected. The
		// request detail view uses its rejections to explain skipped routes.
		_ = recordResolution(r.Context(), p.Repositories, requestID, plan)
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
	officialPrice, officialExpectedCost := officialPricing(planRanked(plan))
	if err := p.execute(r.Context(), w, requestID, sessionID, body, request, canonical, clientFormat, plan, officialPrice, officialExpectedCost); err != nil {
		var partial *partialStreamError
		if errors.As(err, &partial) {
			return
		}
		p.finishError(r.Context(), requestID, persistenceErrorCode(err), sanitize(err.Error()))
		var proxyErr *proxyError
		if errors.As(err, &proxyErr) && len(proxyErr.providerErrors) > 0 {
			writeProviderErrors(w, statusFor(err), errorCode(err), sanitize(err.Error()), proxyErr.attempts, proxyErr.providerErrors)
			return
		}
		writeError(w, statusFor(err), errorCode(err), sanitize(err.Error()))
	}
}

func withoutStructuredOutputParameter(parameters []string) []string {
	result := make([]string, 0, len(parameters))
	for _, parameter := range parameters {
		if strings.EqualFold(strings.TrimSpace(parameter), "response_format") || strings.EqualFold(strings.TrimSpace(parameter), "structured_outputs") {
			continue
		}
		result = append(result, parameter)
	}
	return result
}

// resolveStructuredPlan performs request-time capability discovery only for
// routes that survived normal model and policy ranking. Probe outcomes are
// persisted per route, so later requests use the database marker directly.
func (p *Proxy) resolveStructuredPlan(ctx context.Context, plan routing.Plan) routing.Plan {
	resolved := plan
	resolved.Entries = nil
	deferUnknown := false
	for _, entry := range plan.Entries {
		if deferUnknown {
			// Keep fallback candidates in the plan without probing them now. If
			// the selected route fails, execute() verifies the next candidate
			// immediately before sending its first real request.
			resolved.Entries = append(resolved.Entries, entry)
			continue
		}
		supported, known, err := p.ensureStructuredOutput(ctx, entry.Route)
		if err != nil {
			resolved.Rejections = append(resolved.Rejections, matcher.RouteRejection{RouteID: entry.Route.ID, Code: "structured_output_probe_failed", Detail: err.Error()})
			continue
		}
		if !known || !supported {
			code, detail := "structured_output_unsupported", "route does not support structured output"
			if !known {
				code, detail = "structured_output_unknown", "route structured output capability could not be verified"
			}
			resolved.Rejections = append(resolved.Rejections, matcher.RouteRejection{RouteID: entry.Route.ID, Code: code, Detail: detail})
			continue
		}
		entry.Route.Capabilities.StructuredOutput = true
		if entry.Route.Capabilities.Parameters == nil {
			entry.Route.Capabilities.Parameters = make(map[string]bool)
		}
		entry.Route.Capabilities.Parameters["response_format"] = true
		resolved.Entries = append(resolved.Entries, entry)
		deferUnknown = true
	}
	if len(resolved.Entries) == 0 {
		resolved.Error = &matcher.MatchError{Code: "no_eligible_route", Message: "no healthy compatible route is available"}
	}
	return resolved
}

func (p *Proxy) ensureStructuredOutput(ctx context.Context, route matcher.Route) (supported, known bool, err error) {
	if p.Repositories != nil {
		if value, exists, lookupErr := p.Repositories.ModelRoutes.GetStructuredOutputCapability(ctx, route.ID); lookupErr == nil && exists {
			return value, true, nil
		}
	}
	if route.Capabilities.StructuredOutput {
		return true, true, nil
	}
	client := p.Catalog.ClientForRoute(route)
	prober, ok := client.(providers.StructuredOutputProber)
	if !ok {
		return false, false, nil
	}
	lock := p.routeFormatLock(route.ID)
	lock.Lock()
	defer lock.Unlock()
	// Another request may have completed the probe while this request waited.
	if p.Repositories != nil {
		if value, exists, lookupErr := p.Repositories.ModelRoutes.GetStructuredOutputCapability(ctx, route.ID); lookupErr == nil && exists {
			return value, true, nil
		}
	}
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	probeErr := prober.ProbeStructuredOutput(probeCtx, providers.Model{ID: route.UpstreamModel, Format: route.Format})
	cancel()
	if probeErr != nil {
		var unsupported *providers.StructuredOutputUnsupportedError
		if !errors.As(probeErr, &unsupported) {
			return false, false, probeErr
		}
		p.persistStructuredOutputCapability(ctx, route, false)
		if p.Catalog != nil {
			p.Catalog.LearnStructuredOutput(route.ID, false)
		}
		return false, true, nil
	}
	p.persistStructuredOutputCapability(ctx, route, true)
	if p.Catalog != nil {
		p.Catalog.LearnStructuredOutput(route.ID, true)
	}
	return true, true, nil
}

func (p *Proxy) persistStructuredOutputCapability(ctx context.Context, route matcher.Route, supported bool) error {
	if p.Repositories == nil {
		return nil
	}
	if err := p.Repositories.ModelRoutes.SetStructuredOutputCapability(ctx, route.ID, supported); err == nil {
		return nil
	}
	price, err := json.Marshal(route.Price)
	if err != nil {
		return err
	}
	route.Capabilities.StructuredOutput = supported
	if route.Capabilities.Parameters == nil {
		route.Capabilities.Parameters = make(map[string]bool)
	}
	route.Capabilities.Parameters["response_format"] = supported
	capabilities, err := json.Marshal(route.Capabilities)
	if err != nil {
		return err
	}
	capabilityFields := map[string]json.RawMessage{}
	if err := json.Unmarshal(capabilities, &capabilityFields); err != nil {
		return err
	}
	probeMarker, err := json.Marshal(supported)
	if err != nil {
		return err
	}
	capabilityFields["structured_output_probe"] = probeMarker
	capabilities, err = json.Marshal(capabilityFields)
	if err != nil {
		return err
	}
	if err := p.Repositories.Models.Upsert(ctx, models.ModelRecord{ID: route.LogicalModel, DisplayName: route.LogicalModel, ContextLength: route.Capabilities.MaxContext, MaxOutputTokens: route.Capabilities.MaxOutput, MetadataJSON: "{}", ObservedAt: time.Now().UTC().Format(time.RFC3339Nano)}); err != nil {
		return err
	}
	return p.Repositories.ModelRoutes.Upsert(ctx, models.ModelRouteRecord{ID: route.ID, ModelID: route.LogicalModel, Provider: route.Provider, UpstreamModel: route.UpstreamModel, Format: string(route.Format), PriceJSON: string(price), CapabilitiesJSON: string(capabilities), Health: string(route.Health), ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), Trusted: route.Trusted})
}

type parsedRequest struct {
	Protocol                 matcher.Protocol
	Model                    string
	InputTokens              int64
	ExpectedOutput           int64
	MaxContext               int64
	MaxOutput                int64
	RequiredParams           []string
	RequireStructured        bool
	Stream                   bool
	RequiredInputModalities  []string
	RequiredOutputModalities []string
}

func (p parsedRequest) MatchRequest(protocol matcher.Protocol) matcher.MatchRequest {
	return matcher.MatchRequest{Protocol: protocol, LogicalModel: p.Model, RequiredParameters: p.RequiredParams, RequireStructured: p.RequireStructured, InputTokens: p.InputTokens, ExpectedOutput: p.ExpectedOutput, MaxContext: p.MaxContext, MaxOutput: p.MaxOutput, RequiredInputModalities: p.RequiredInputModalities, RequiredOutputModalities: p.RequiredOutputModalities}
}

func parseRequest(body []byte, protocol matcher.Protocol, canonical *wire.Request) (parsedRequest, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return parsedRequest{}, fmt.Errorf("body must be valid JSON: %w", err)
	}
	if canonical == nil {
		return parsedRequest{}, errors.New("canonical request is required")
	}
	request := parsedRequest{Protocol: protocol}
	request.Model = canonical.Model
	request.Stream = canonical.Options.Stream
	if canonical.Options.MaxOutputTokens != nil {
		request.ExpectedOutput = *canonical.Options.MaxOutputTokens
	}
	if request.ExpectedOutput == 0 {
		request.ExpectedOutput = 256
	}
	request.InputTokens = int64(len(body) / 4)
	if request.InputTokens == 0 {
		request.InputTokens = 1
	}
	if canonical.Options.StructuredOutput != nil {
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
	request.RequiredOutputModalities = normalizeModalities(canonical.Options.Modalities)
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

func (p *Proxy) execute(ctx context.Context, writer http.ResponseWriter, requestID, sessionID string, body []byte, request parsedRequest, canonical *wire.Request, clientFormat wire.Format, plan routing.Plan, officialPrice matcher.Price, officialExpectedCost int64) error {
	current := 0
	blocked := false
	policy := retry.DefaultPolicy()
	policy.MaximumAttempts = routing.DefaultLimits().MaximumAttempts
	totalAttempts := 0
	retriesRemaining := -1
	providerErrors := []providerError{}
	lastAttemptErrorCode := ""
	for totalAttempts < policy.MaximumAttempts {
		if current >= len(plan.Entries) {
			if blocked {
				if len(providerErrors) > 0 {
					return allProviderAttemptsFailed(http.StatusTooManyRequests, totalAttempts, providerErrors, lastAttemptErrorCode)
				}
				return &proxyError{status: http.StatusTooManyRequests, code: "all_subscription_quotas_exhausted", message: "all eligible subscription provider accounts are temporarily limited"}
			}
			if len(providerErrors) > 0 {
				return allProviderAttemptsFailed(http.StatusServiceUnavailable, totalAttempts, providerErrors, lastAttemptErrorCode)
			}
			return &proxyError{status: http.StatusServiceUnavailable, code: "no_fallback_route", message: "all eligible routes were exhausted"}
		}
		entry := plan.Entries[current]
		route := entry.Route
		if retriesRemaining < 0 {
			retriesRemaining = entry.SameRouteRetries
		}
		if request.RequireStructured && !route.Capabilities.StructuredOutput {
			supported, known, probeErr := p.ensureStructuredOutput(ctx, route)
			if probeErr != nil {
				lastAttemptErrorCode = "structured_output_probe_failed"
				providerErrors = append(providerErrors, providerError{Provider: route.Provider, Account: route.Account, Error: probeErr.Error()})
			}
			if !known || !supported {
				current++
				retriesRemaining = -1
				continue
			}
			route.Capabilities.StructuredOutput = true
			if route.Capabilities.Parameters == nil {
				route.Capabilities.Parameters = make(map[string]bool)
			}
			route.Capabilities.Parameters["response_format"] = true
			entry.Route = route
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
			totalAttempts++
			lastAttemptErrorCode = "provider_not_configured"
			providerErrors = append(providerErrors, providerError{Provider: route.Provider, Account: route.Account, Error: "selected provider is not configured"})
			if p.Repositories != nil {
				_ = recordProxyAttemptRoute(ctx, p.Repositories, requestID, totalAttempts, route.ID, route.CredentialID, entry.StageID, strings.Join(entry.StagePath, " / "), route.Provider, route.UpstreamModel, "failed", "provider_not_configured", "Selected provider is not configured.", nil, "selected provider is not configured")
			}
			current++
			retriesRemaining = -1
			continue
		}
		totalAttempts++
		if p.Repositories != nil {
			_ = recordProxyAttemptRoute(ctx, p.Repositories, requestID, totalAttempts, route.ID, route.CredentialID, entry.StageID, strings.Join(entry.StagePath, " / "), route.Provider, route.UpstreamModel, "started", "", "", nil)
		}
		translated, translatedOK := client.(providers.TranslationClient)
		if gate, hasGate := client.(interface{ TranslationEnabled() bool }); hasGate && !gate.TranslationEnabled() {
			translatedOK = false
		}
		if translatedOK && canonical != nil {
			lastErr, completed, attemptsUsed := p.executeTranslatedRoute(ctx, writer, requestID, sessionID, request, canonical, clientFormat, translated, route, entry, totalAttempts, policy.MaximumAttempts-totalAttempts, officialPrice, officialExpectedCost)
			totalAttempts += attemptsUsed - 1
			if completed {
				return lastErr
			}
			// Format discovery already consumed the route's available candidate
			// attempts. Feed the terminal error back through the same provider
			// bookkeeping and fallback policy as the legacy execution path.
			if lastErr != nil && p.Repositories != nil {
				_ = p.Repositories.ProxyRequests.RecordAttemptRoute(ctx, requestID, totalAttempts, route.Provider, route.UpstreamModel)
			}
			if lastErr == nil {
				current++
				retriesRemaining = -1
				continue
			}
			classified := classify(lastErr)
			if classified.Class == retry.ErrorQuotaExhausted {
				blocked = true
				var upstream *providers.UpstreamError
				if errors.As(lastErr, &upstream) {
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
			lastAttemptErrorCode = errorCode(lastErr)
			providerErrors = append(providerErrors, providerError{Provider: route.Provider, Account: route.Account, Error: humanErrorMessage(lastErr)})
			decision := p.Retry.Decide(retry.Input{Policy: policy, AttemptNumber: totalAttempts, Now: time.Now(), Error: classified, Delivery: retry.NothingSent, SameRouteAvailable: !route.Free, FallbacksRemaining: len(plan.Entries) - current - 1, PlanMode: true, SameRouteRetriesRemaining: retriesRemaining, PlanEntriesRemaining: len(plan.Entries) - current - 1, TotalAttemptsRemaining: policy.MaximumAttempts - totalAttempts})
			if decision.Action != retry.RetrySameRoute && decision.Action != retry.FailOver && len(plan.Entries)-current-1 > 0 && classified.Class != retry.ErrorCancelled {
				decision.Action = retry.FailOver
				decision.Delay = 0
			}
			if decision.Action != retry.RetrySameRoute && decision.Action != retry.FailOver {
				return allProviderAttemptsFailed(statusFor(lastErr), totalAttempts, providerErrors, lastAttemptErrorCode)
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
			continue
		}
		response, err := client.Do(ctx, request.Protocol, route.UpstreamModel, body, sessionID)
		if err == nil {
			if request.Stream || strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream") {
				streamErr := p.stream(ctx, writer, requestID, response, entry.ExpectedCost, officialExpectedCost, route.Price, officialPrice)
				if p.Repositories != nil {
					state, code := streamOutcome(streamErr)
					message := ""
					raw := ""
					if streamErr != nil {
						message, raw = humanErrorMessage(streamErr), sanitize(streamErr.Error())
					}
					finalCtx, cancel := streamFinalizeContext(ctx)
					_ = recordProxyAttemptRoute(finalCtx, p.Repositories, requestID, totalAttempts, route.ID, route.CredentialID, entry.StageID, strings.Join(entry.StagePath, " / "), route.Provider, route.UpstreamModel, state, code, message, httpStatusPointer(response.StatusCode), raw)
					cancel()
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
				_ = recordProxyAttemptRoute(ctx, p.Repositories, requestID, totalAttempts, route.ID, route.CredentialID, entry.StageID, strings.Join(entry.StagePath, " / "), route.Provider, route.UpstreamModel, state, code, message, httpStatusPointer(response.StatusCode), raw)
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
			_ = recordProxyAttemptRoute(ctx, p.Repositories, requestID, totalAttempts, route.ID, route.CredentialID, entry.StageID, strings.Join(entry.StagePath, " / "), route.Provider, route.UpstreamModel, "failed", errorCode(err), humanErrorMessage(err), upstreamHTTPStatus(err), rawErrorMessage(err))
		}
		lastAttemptErrorCode = errorCode(err)
		providerErrors = append(providerErrors, providerError{Provider: route.Provider, Account: route.Account, Error: humanErrorMessage(err)})
		decision := p.Retry.Decide(retry.Input{Policy: policy, AttemptNumber: totalAttempts, Now: time.Now(), Error: classified, Delivery: retry.NothingSent, SameRouteAvailable: !route.Free, FallbacksRemaining: len(plan.Entries) - current - 1, PlanMode: true, SameRouteRetriesRemaining: retriesRemaining, PlanEntriesRemaining: len(plan.Entries) - current - 1, TotalAttemptsRemaining: policy.MaximumAttempts - totalAttempts})
		// A provider error must not hide healthy routes later in the plan. The
		// retry engine still controls configured same-route retries, but if it
		// would otherwise return a terminal decision, advance to the next
		// provider while no response bytes have been sent.
		if decision.Action != retry.RetrySameRoute && decision.Action != retry.FailOver && len(plan.Entries)-current-1 > 0 && classified.Class != retry.ErrorCancelled {
			decision.Action = retry.FailOver
			decision.Delay = 0
		}
		if decision.Action != retry.RetrySameRoute && decision.Action != retry.FailOver {
			return allProviderAttemptsFailed(statusFor(err), totalAttempts, providerErrors, lastAttemptErrorCode)
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
	return allProviderAttemptsFailed(http.StatusBadGateway, totalAttempts, providerErrors, lastAttemptErrorCode)
}

func (p *Proxy) executeTranslatedRoute(ctx context.Context, writer http.ResponseWriter, requestID, sessionID string, request parsedRequest, canonical *wire.Request, clientFormat wire.Format, client providers.TranslationClient, route matcher.Route, entry routing.Entry, firstAttempt, remainingBudget int, officialPrice matcher.Price, officialExpectedCost int64) (error, bool, int) {
	lock := p.routeFormatLock(route.ID)
	lock.Lock()
	defer lock.Unlock()
	if p.Catalog != nil {
		for _, current := range p.Catalog.Snapshot().Routes {
			if current.ID == route.ID {
				route.Format = current.Format
				break
			}
		}
	}
	saved := route.Format
	if p.Repositories != nil {
		if format, ok, err := p.Repositories.ModelRoutes.GetFormat(ctx, route.ID); err == nil && ok {
			saved = format
		}
	}
	hinted := client.Endpoint().HintedFormat
	formatKnown := saved.Valid() || hinted.Valid()
	candidates := providers.CandidateFormats(saved, hinted, clientFormat)
	if formatKnown {
		known := saved
		if !known.Valid() {
			known = hinted
		}
		candidates = []wire.Format{known}
	}
	if remainingBudget < 0 {
		remainingBudget = 0
	}
	if len(candidates) > remainingBudget+1 {
		candidates = candidates[:remainingBudget+1]
	}
	lastErr := error(nil)
	formatFailure := false
	used := 0
	for index, candidate := range candidates {
		attempt := firstAttempt + index
		if index > 0 {
			used++
		}
		if index == 0 {
			used = 1
		}
		if err := candidate.Validate(); err != nil {
			lastErr = err
			p.recordTranslatedAttempt(ctx, requestID, attempt, route, entry, clientFormat, candidate, "failed", "local", err)
			continue
		}
		encoded, err := wire.EncodeRequest(candidate, canonical)
		if err != nil {
			lastErr = err
			p.recordTranslatedAttempt(ctx, requestID, attempt, route, entry, clientFormat, candidate, "failed", "local", err)
			continue
		}
		prepared, err := client.Prepare(candidate, route.UpstreamModel, encoded, sessionID)
		if err != nil {
			lastErr = err
			p.recordTranslatedAttempt(ctx, requestID, attempt, route, entry, clientFormat, candidate, "failed", "local", err)
			continue
		}
		p.recordTranslatedAttempt(ctx, requestID, attempt, route, entry, clientFormat, candidate, "started", "", nil)
		response, err := client.DoPrepared(ctx, prepared)
		if err != nil {
			lastErr = err
			p.recordTranslatedAttempt(ctx, requestID, attempt, route, entry, clientFormat, candidate, "failed", errorCode(err), err)
			if !shouldTryNextFormat(err, !formatKnown) {
				break
			}
			formatFailure = true
			continue
		}
		if request.Stream && wire.IsStreamResponse(response) {
			result := pumpTranslatedStream(writer, clientFormat, candidate, response)
			if result.err != nil && !result.committed {
				lastErr = result.err
				p.recordTranslatedAttempt(ctx, requestID, attempt, route, entry, clientFormat, candidate, "failed", "malformed_response", result.err)
				if !shouldTryNextFormat(result.err, !formatKnown) {
					break
				}
				formatFailure = true
				continue
			}
			streamErr := p.finalizeStream(ctx, requestID, result.stats, entry.ExpectedCost, officialExpectedCost, route.Price, officialPrice, result.err)
			if streamErr == nil {
				p.learnTranslatedFormat(ctx, route, candidate)
			}
			p.recordTranslatedStreamAttempt(ctx, requestID, attempt, route, entry, clientFormat, candidate, streamErr)
			return streamErr, true, used
		}
		events, decodeErr := wire.DecodeResponse(candidate, response)
		if decodeErr != nil {
			var partial *wire.PartialResponseError
			if errors.As(decodeErr, &partial) && len(events.Events) > 0 {
				streamErr := p.completeTranslatedStream(ctx, writer, requestID, events, entry.ExpectedCost, officialExpectedCost, route.Price, officialPrice, clientFormat, decodeErr)
				p.recordTranslatedStreamAttempt(ctx, requestID, attempt, route, entry, clientFormat, candidate, streamErr)
				return streamErr, true, used
			}
			lastErr = decodeErr
			p.recordTranslatedAttempt(ctx, requestID, attempt, route, entry, clientFormat, candidate, "failed", "malformed_response", decodeErr)
			if !shouldTryNextFormat(decodeErr, !formatKnown) {
				break
			}
			formatFailure = true
			continue
		}
		p.learnTranslatedFormat(ctx, route, candidate)
		if request.Stream {
			streamErr := p.completeTranslatedStream(ctx, writer, requestID, events, entry.ExpectedCost, officialExpectedCost, route.Price, officialPrice, clientFormat, nil)
			p.recordTranslatedStreamAttempt(ctx, requestID, attempt, route, entry, clientFormat, candidate, streamErr)
			return streamErr, true, used
		}
		completeErr := p.completeTranslated(ctx, writer, requestID, events, entry.ExpectedCost, officialExpectedCost, route.Price, officialPrice, clientFormat)
		if completeErr != nil {
			p.recordTranslatedAttempt(ctx, requestID, attempt, route, entry, clientFormat, candidate, "failed", errorCode(completeErr), completeErr)
			return completeErr, true, used
		}
		p.recordTranslatedAttempt(ctx, requestID, attempt, route, entry, clientFormat, candidate, "succeeded", "", nil)
		return nil, true, used
	}
	if lastErr == nil {
		lastErr = &proxyError{status: http.StatusBadGateway, code: "no_supported_provider_format", message: "no supported provider format could be used"}
	}
	if formatFailure {
		if p.Repositories != nil {
			_ = p.Repositories.ModelRoutes.ClearFormat(ctx, route.ID)
		}
		if p.Catalog != nil {
			p.Catalog.ClearFormat(route.ID)
		}
	}
	return lastErr, false, used
}

func (p *Proxy) routeFormatLock(routeID string) *sync.Mutex {
	p.formatMu.Lock()
	defer p.formatMu.Unlock()
	if p.formatLocks == nil {
		p.formatLocks = make(map[string]*sync.Mutex)
	}
	lock := p.formatLocks[routeID]
	if lock == nil {
		lock = &sync.Mutex{}
		p.formatLocks[routeID] = lock
	}
	return lock
}

func persistLearnedFormat(ctx context.Context, repos *repositories.Repositories, route matcher.Route, format wire.Format) error {
	if err := repos.ModelRoutes.SetFormat(ctx, route.ID, format); err == nil {
		return nil
	}
	price, err := json.Marshal(route.Price)
	if err != nil {
		return err
	}
	capabilities, err := json.Marshal(route.Capabilities)
	if err != nil {
		return err
	}
	if err := repos.Models.Upsert(ctx, models.ModelRecord{ID: route.LogicalModel, DisplayName: route.LogicalModel, ContextLength: route.Capabilities.MaxContext, MaxOutputTokens: route.Capabilities.MaxOutput, MetadataJSON: "{}", ObservedAt: time.Now().UTC().Format(time.RFC3339Nano)}); err != nil {
		return err
	}
	return repos.ModelRoutes.Upsert(ctx, models.ModelRouteRecord{ID: route.ID, ModelID: route.LogicalModel, Provider: route.Provider, UpstreamModel: route.UpstreamModel, Format: string(format), PriceJSON: string(price), CapabilitiesJSON: string(capabilities), Health: string(route.Health), ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), Trusted: route.Trusted})
}

func (p *Proxy) recordTranslatedAttempt(ctx context.Context, requestID string, attempt int, route matcher.Route, entry routing.Entry, clientFormat, providerFormat wire.Format, state, code string, err error) {
	if p.Repositories == nil {
		return
	}
	message, raw := "", ""
	if err != nil {
		message, raw = humanErrorMessage(err), sanitize(err.Error())
	}
	status := upstreamHTTPStatus(err)
	if status == nil && (state == "succeeded" || state == "partial") {
		status = httpStatusPointer(http.StatusOK)
	}
	_ = recordProxyAttemptRouteFormats(ctx, p.Repositories, requestID, attempt, route.ID, route.CredentialID, entry.StageID, strings.Join(entry.StagePath, " / "), route.Provider, route.UpstreamModel, state, code, message, clientFormat, providerFormat, status, raw)
}

// shouldTryNextFormat allows format discovery to use endpoint-related upstream
// responses as evidence when the route has no known format. Some providers
// report an unsupported endpoint as a generic 500, so server errors must be
// probeable too. Authentication, billing, quota, timeout, conflict, and
// payload-size errors are not format evidence and must not trigger extra
// requests. Once a format is known, errors use normal retry/failover.
func shouldTryNextFormat(err error, unknownFormat bool) bool {
	if !unknownFormat {
		return false
	}
	if wire.IsIncompatibility(err) {
		return true
	}
	var malformed *wire.MalformedResponseError
	if errors.As(err, &malformed) {
		return true
	}
	var upstream *providers.UpstreamError
	if !errors.As(err, &upstream) {
		return false
	}
	switch upstream.StatusCode {
	case http.StatusBadRequest,
		http.StatusNotFound,
		http.StatusMethodNotAllowed,
		http.StatusNotAcceptable,
		http.StatusUnsupportedMediaType,
		http.StatusUnprocessableEntity:
		return true
	default:
		return upstream.StatusCode >= http.StatusInternalServerError && upstream.StatusCode <= 599
	}
}

func (p *Proxy) completeTranslated(ctx context.Context, writer http.ResponseWriter, requestID string, events wire.EventStream, expectedCost, officialExpectedCost int64, price, officialPrice matcher.Price, clientFormat wire.Format) error {
	if err := wire.EncodeResponse(clientFormat, events, writer); err != nil {
		return err
	}
	stats := usage.Stats{}
	for _, event := range events.Events {
		if event.Usage != nil {
			mergeWireUsage(&stats, *event.Usage)
		}
		if event.Response != nil {
			mergeWireUsage(&stats, event.Response.Usage)
		}
	}
	persistUsage(ctx, p.Repositories, requestID, stats, expectedCost, officialExpectedCost, price, officialPrice)
	if p.Repositories != nil {
		_ = p.Repositories.ProxyRequests.Complete(ctx, requestID, "succeeded", "", "")
	}
	return nil
}

// mergeWireUsage keeps sparse lifecycle events from erasing a previously
// observed snapshot. Provider usage values are cumulative snapshots, so a
// later non-zero value replaces an earlier one rather than being added.
func mergeWireUsage(dst *usage.Stats, src wire.Usage) {
	if src.InputTokens != 0 {
		dst.InputTokens = src.InputTokens
	}
	if src.OutputTokens != 0 {
		dst.OutputTokens = src.OutputTokens
	}
	if src.TotalTokens >= src.InputTokens+src.OutputTokens && src.TotalTokens >= dst.TotalTokens {
		dst.TotalTokens = src.TotalTokens
	}
	if src.CachedReadTokens != 0 {
		dst.CachedReadTokens = src.CachedReadTokens
	}
	if src.CacheWriteTokens != 0 {
		dst.CacheWriteTokens = src.CacheWriteTokens
	}
	if src.ReasoningTokens != 0 {
		dst.ReasoningTokens = src.ReasoningTokens
	}
	if src.InputTokensNetOfCache {
		dst.InputTokensNetOfCache = true
	}
	total := dst.InputTokens + dst.OutputTokens
	if dst.InputTokensNetOfCache {
		total += dst.CachedReadTokens + dst.CacheWriteTokens
	}
	dst.TotalTokens = max(dst.TotalTokens, total)
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

func upstreamHTTPStatus(err error) *int {
	var upstream *providers.UpstreamError
	if !errors.As(err, &upstream) || upstream.StatusCode < 100 || upstream.StatusCode > 599 {
		return nil
	}
	status := upstream.StatusCode
	return &status
}

func httpStatusPointer(status int) *int { return &status }

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
	status         int
	code           string
	message        string
	attempts       int
	providerErrors []providerError
	terminalCode   string
}

type providerError struct {
	Provider string `json:"provider"`
	Account  string `json:"account"`
	Error    string `json:"error"`
}

func allProviderAttemptsFailed(status, attempts int, providerErrors []providerError, terminalCode string) *proxyError {
	return &proxyError{
		status:         status,
		code:           "all_provider_attempts_failed",
		message:        "all provider attempts failed",
		attempts:       attempts,
		providerErrors: providerErrors,
		terminalCode:   terminalCode,
	}
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

func persistenceErrorCode(err error) string {
	var proxyErr *proxyError
	if errors.As(err, &proxyErr) && proxyErr.terminalCode != "" {
		return proxyErr.terminalCode
	}
	return errorCode(err)
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

func writeProviderErrors(w http.ResponseWriter, status int, code, message string, attempts int, providerErrors []providerError) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"type": "payless_error", "code": code, "message": message, "attempts": attempts, "errors": providerErrors}})
}

func sanitize(message string) string {
	message = strings.Join(strings.Fields(message), " ")
	if len(message) > 1024 {
		return message[:1024]
	}
	return message
}
