// Package matcher contains the deterministic route selection engine.
package matcher

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Protocol identifies the wire protocol required by a request.
type Protocol string

const (
	ProtocolChatCompletions Protocol = "chat_completions"
	ProtocolResponses       Protocol = "responses"
	ProtocolAnthropic       Protocol = "anthropic_messages"
)

// Health is the persisted eligibility state of a route or credential.
type Health string

const (
	HealthHealthy  Health = "healthy"
	HealthBackoff  Health = "backoff"
	HealthDisabled Health = "disabled"
)

// BillingClass describes the marginal billing source of a route. It is kept
// in matcher so the pure engine does not depend on persistence or groups.
type BillingClass string

const (
	BillingFree         BillingClass = "free"
	BillingSubscription BillingClass = "subscription"
	BillingMetered      BillingClass = "metered"
)

// Price stores USD prices in pico-USD per token. Fixed-point values keep route
// selection deterministic and avoid binary floating-point surprises.
type Price struct {
	InputPicoUSDPerToken      int64
	OutputPicoUSDPerToken     int64
	CachedReadPicoUSDPerToken int64
	CacheWritePicoUSDPerToken int64
	ReasoningPicoUSDPerToken  int64
	FixedPicoUSD              int64
	ObservedAt                time.Time
	StaleAt                   time.Time
}

type UsageCostInput struct {
	InputTokens           int64
	OutputTokens          int64
	CachedReadTokens      int64
	CacheWriteTokens      int64
	ReasoningTokens       int64
	InputTokensNetOfCache bool
}

// Capabilities describes the protocol and request parameters a route accepts.
type Capabilities struct {
	Protocols        map[Protocol]bool
	Parameters       map[string]bool
	Tools            bool
	StructuredOutput bool
	MaxContext       int64
	MaxOutput        int64
	InputModalities  map[string]bool
	OutputModalities map[string]bool
	Tags             []string
}

// Route is a fully materialized candidate. MatchEngine does not fetch or
// mutate route state; callers provide a snapshot.
type Route struct {
	ID                     string
	Provider               string
	LogicalModel           string
	UpstreamModel          string
	Free                   bool
	Price                  Price
	PriceAvailable         bool
	OfficialPrice          Price
	OfficialPriceAvailable bool
	Capabilities           Capabilities
	Health                 Health
	Trusted                bool
	SuccessRateBPS         int64
	LatencyMillisP50       int64
	CredentialID           string
	ExecutionKey           string
	BillingClass           BillingClass
}

// MatchRequest contains only facts needed by the matcher.
type MatchRequest struct {
	Protocol                     Protocol
	LogicalModel                 string
	LogicalModels                []string
	RequiredParameters           []string
	RequireTools                 bool
	RequireStructured            bool
	InputTokens                  int64
	ExpectedOutput               int64
	MaxContext                   int64
	MaxOutput                    int64
	AllowStale                   bool
	AllowUntrusted               bool
	AllowedProviders             []string
	ExcludedProviders            []string
	MaximumCostPicoUSD           *int64
	MaximumInputPicoUSDPerToken  *int64
	MaximumOutputPicoUSDPerToken *int64
	MaximumOfficialPricePercent  *int
	AllowedBillingClasses        []BillingClass
	RequiredInputModalities      []string
	RequiredOutputModalities     []string
}

type MatchInput struct {
	Request MatchRequest
	Routes  []Route
	Now     time.Time
}

type RankedRoute struct {
	Route        Route
	ExpectedCost int64
}

type RouteRejection struct {
	RouteID string `json:"route_id"`
	Code    string `json:"code"`
	Detail  string `json:"detail"`
}

type MatchError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *MatchError) Error() string { return e.Code + ": " + e.Message }

type MatchResult struct {
	Selected   *RankedRoute
	Ranked     []RankedRoute
	Rejections []RouteRejection
	Error      *MatchError
}

type Engine struct{}

func New() Engine { return Engine{} }

// Match returns a stable, explainable ranking. It never performs I/O and does
// not depend on route order.
func (Engine) Match(input MatchInput) MatchResult {
	result := MatchResult{}
	if input.Request.InputTokens < 0 || input.Request.ExpectedOutput < 0 || input.Request.MaxContext < 0 || input.Request.MaxOutput < 0 {
		result.Error = &MatchError{Code: "invalid_estimate", Message: "token and limit estimates must be non-negative"}
		return result
	}
	if input.Request.MaximumCostPicoUSD != nil && *input.Request.MaximumCostPicoUSD < 0 {
		result.Error = &MatchError{Code: "invalid_cost_limit", Message: "maximum cost must be non-negative"}
		return result
	}
	if input.Request.MaximumOfficialPricePercent != nil && (*input.Request.MaximumOfficialPricePercent < 0 || *input.Request.MaximumOfficialPricePercent > 100) {
		result.Error = &MatchError{Code: "invalid_price_percent", Message: "official price percentage must be between 0 and 100"}
		return result
	}

	allowed := providerSet(input.Request.AllowedProviders)
	excluded := providerSet(input.Request.ExcludedProviders)
	for _, route := range input.Routes {
		rejection, ok := rejectRoute(input.Request, route, input.Now, allowed, excluded)
		if !ok {
			cost, err := expectedCost(input.Request.InputTokens, input.Request.ExpectedOutput, route.Price)
			if err != nil {
				result.Rejections = append(result.Rejections, RouteRejection{RouteID: route.ID, Code: "price_overflow", Detail: err.Error()})
				continue
			}
			if input.Request.MaximumCostPicoUSD != nil && cost > *input.Request.MaximumCostPicoUSD {
				result.Rejections = append(result.Rejections, RouteRejection{RouteID: route.ID, Code: "over_maximum_cost", Detail: fmt.Sprintf("expected cost %d exceeds limit %d", cost, *input.Request.MaximumCostPicoUSD)})
				continue
			}
			result.Ranked = append(result.Ranked, RankedRoute{Route: route, ExpectedCost: cost})
			continue
		}
		result.Rejections = append(result.Rejections, rejection)
	}

	sort.SliceStable(result.Ranked, func(i, j int) bool {
		a, b := result.Ranked[i], result.Ranked[j]
		if a.Route.Free != b.Route.Free {
			return a.Route.Free
		}
		if a.ExpectedCost != b.ExpectedCost {
			return a.ExpectedCost < b.ExpectedCost
		}
		if !a.Route.Price.ObservedAt.Equal(b.Route.Price.ObservedAt) {
			return a.Route.Price.ObservedAt.After(b.Route.Price.ObservedAt)
		}
		if a.Route.SuccessRateBPS != b.Route.SuccessRateBPS {
			return a.Route.SuccessRateBPS > b.Route.SuccessRateBPS
		}
		if a.Route.LatencyMillisP50 != b.Route.LatencyMillisP50 {
			return a.Route.LatencyMillisP50 < b.Route.LatencyMillisP50
		}
		return a.Route.ID < b.Route.ID
	})
	if len(result.Ranked) > 0 {
		result.Selected = &result.Ranked[0]
	} else {
		result.Error = &MatchError{Code: "no_eligible_route", Message: "no healthy compatible route is available"}
	}
	return result
}

func rejectRoute(request MatchRequest, route Route, now time.Time, allowed, excluded map[string]struct{}) (RouteRejection, bool) {
	reject := func(code, detail string) (RouteRejection, bool) {
		return RouteRejection{RouteID: route.ID, Code: code, Detail: detail}, true
	}
	if !modelAllowed(route.LogicalModel, request) {
		return reject("wrong_model", "route model does not match the requested logical model")
	}
	if !route.Capabilities.Protocols[request.Protocol] {
		return reject("unsupported_protocol", "route does not support the requested wire protocol")
	}
	if len(allowed) > 0 {
		if _, ok := allowed[strings.ToLower(route.Provider)]; !ok {
			return reject("provider_not_allowed", "provider is outside the allow-list")
		}
	}
	if _, ok := excluded[strings.ToLower(route.Provider)]; ok {
		return reject("provider_excluded", "provider is on the deny-list")
	}
	if len(request.AllowedBillingClasses) > 0 {
		billing := route.BillingClass
		if billing == "" {
			if route.Free {
				billing = BillingFree
			} else {
				billing = BillingMetered
			}
		}
		allowedBilling := false
		for _, candidate := range request.AllowedBillingClasses {
			if candidate == billing {
				allowedBilling = true
				break
			}
		}
		if !allowedBilling {
			return reject("wrong_billing_class", "route billing class is outside the stage policy")
		}
	}
	if request.RequireTools && !route.Capabilities.Tools {
		return reject("missing_capability", "route does not support tools")
	}
	if request.RequireStructured && !route.Capabilities.StructuredOutput {
		return reject("missing_capability", "route does not support structured output")
	}
	for _, parameter := range request.RequiredParameters {
		if !route.Capabilities.Parameters[parameter] {
			return reject("missing_capability", "route does not support parameter "+parameter)
		}
	}
	for _, modality := range request.RequiredInputModalities {
		if len(route.Capabilities.InputModalities) > 0 && !route.Capabilities.InputModalities[strings.ToLower(strings.TrimSpace(modality))] {
			return reject("missing_modality", "route does not support input modality "+modality)
		}
	}
	for _, modality := range request.RequiredOutputModalities {
		if len(route.Capabilities.OutputModalities) > 0 && !route.Capabilities.OutputModalities[strings.ToLower(strings.TrimSpace(modality))] {
			return reject("missing_modality", "route does not support output modality "+modality)
		}
	}
	if request.MaxContext > 0 && route.Capabilities.MaxContext < request.MaxContext {
		return reject("context_too_small", "route context limit is smaller than requested")
	}
	if request.MaxOutput > 0 && route.Capabilities.MaxOutput < request.MaxOutput {
		return reject("output_limit_too_small", "route output limit is smaller than requested")
	}
	if route.Health == HealthDisabled {
		return reject("credential_disabled", "route credential is disabled")
	}
	if route.Health == HealthBackoff {
		return reject("circuit_open", "route is in health backoff")
	}
	if !request.AllowUntrusted && !route.Trusted {
		return reject("untrusted_route", "route is not trusted")
	}
	if !request.AllowStale && !route.Price.StaleAt.IsZero() && !now.Before(route.Price.StaleAt) {
		return reject("stale_price", "route price snapshot is stale")
	}
	if !route.PriceAvailable {
		return reject("missing_price", "route does not expose usable pricing")
	}
	if route.Price.InputPicoUSDPerToken < 0 || route.Price.OutputPicoUSDPerToken < 0 || route.Price.FixedPicoUSD < 0 {
		return reject("missing_price", "route has invalid negative pricing")
	}
	if request.MaximumOfficialPricePercent != nil && strings.EqualFold(route.Provider, "surplus") {
		if !route.OfficialPriceAvailable || route.OfficialPrice.InputPicoUSDPerToken < 0 || route.OfficialPrice.OutputPicoUSDPerToken < 0 {
			return reject("missing_official_price", "auction route does not expose an official price")
		}
		percent := int64(*request.MaximumOfficialPricePercent)
		if percent > 0 && (route.OfficialPrice.InputPicoUSDPerToken > maxInt64/percent || route.OfficialPrice.OutputPicoUSDPerToken > maxInt64/percent) {
			return reject("invalid_official_price", "official price is too large for auction percentage")
		}
		inputCap := route.OfficialPrice.InputPicoUSDPerToken * percent / 100
		outputCap := route.OfficialPrice.OutputPicoUSDPerToken * percent / 100
		if route.Price.InputPicoUSDPerToken > inputCap || route.Price.OutputPicoUSDPerToken > outputCap {
			return reject("over_official_price_limit", "auction price exceeds the allowed percentage of official price")
		}
	}
	if request.MaximumInputPicoUSDPerToken != nil && route.Price.InputPicoUSDPerToken > *request.MaximumInputPicoUSDPerToken {
		return reject("over_input_price_limit", "route input price exceeds the stage limit")
	}
	if request.MaximumOutputPicoUSDPerToken != nil && route.Price.OutputPicoUSDPerToken > *request.MaximumOutputPicoUSDPerToken {
		return reject("over_output_price_limit", "route output price exceeds the stage limit")
	}
	return RouteRejection{}, false
}

const maxInt64 = int64(^uint64(0) >> 1)

func modelAllowed(routeModel string, request MatchRequest) bool {
	if len(request.LogicalModels) == 0 {
		return sameLogicalModel(routeModel, request.LogicalModel)
	}
	for _, model := range request.LogicalModels {
		if sameLogicalModel(routeModel, model) {
			return true
		}
	}
	return false
}

func sameLogicalModel(routeModel, requestedModel string) bool {
	return canonicalModelID(routeModel) == canonicalModelID(requestedModel)
}

func canonicalModelID(value string) string {
	return strings.TrimSuffix(strings.TrimSpace(value), ":free")
}

func expectedCost(inputTokens, outputTokens int64, price Price) (int64, error) {
	return estimateParts([]int64{inputTokens, price.InputPicoUSDPerToken, outputTokens, price.OutputPicoUSDPerToken, 1, price.FixedPicoUSD})
}

func EstimateUsageCost(usage UsageCostInput, price Price) (int64, error) {
	values := []int64{usage.InputTokens, usage.OutputTokens, usage.CachedReadTokens, usage.CacheWriteTokens, usage.ReasoningTokens}
	for _, value := range values {
		if value < 0 {
			return 0, fmt.Errorf("usage values must be non-negative")
		}
	}
	inputTokens := usage.InputTokens
	if !usage.InputTokensNetOfCache {
		if usage.CachedReadTokens >= inputTokens {
			inputTokens = 0
		} else {
			inputTokens -= usage.CachedReadTokens
		}
	}
	return estimateParts([]int64{
		inputTokens, price.InputPicoUSDPerToken,
		usage.CachedReadTokens, price.CachedReadPicoUSDPerToken,
		usage.CacheWriteTokens, price.CacheWritePicoUSDPerToken,
		usage.OutputTokens, price.OutputPicoUSDPerToken,
		usage.ReasoningTokens, price.ReasoningPicoUSDPerToken,
		1, price.FixedPicoUSD,
	})
}

func estimateParts(parts []int64) (int64, error) {
	if len(parts)%2 != 0 {
		return 0, fmt.Errorf("invalid cost parts")
	}
	maximum := int64(^uint64(0) >> 1)
	total := int64(0)
	for index := 0; index < len(parts); index += 2 {
		quantity, price := parts[index], parts[index+1]
		if quantity < 0 || price < 0 {
			return 0, fmt.Errorf("cost values must be non-negative")
		}
		if quantity != 0 && price > maximum/quantity {
			return 0, fmt.Errorf("price multiplication overflow")
		}
		amount := quantity * price
		if amount > maximum-total {
			return 0, fmt.Errorf("price addition overflow")
		}
		total += amount
	}
	return total, nil
}

func providerSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		if normalized := strings.ToLower(strings.TrimSpace(value)); normalized != "" {
			result[normalized] = struct{}{}
		}
	}
	return result
}
