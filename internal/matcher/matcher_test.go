package matcher

import (
	"testing"
	"time"
)

func testRoute(id, provider string, input, output int64) Route {
	return Route{
		ID: id, Provider: provider, LogicalModel: "model-a", UpstreamModel: id,
		Price:          Price{InputPicoUSDPerToken: input, OutputPicoUSDPerToken: output, ObservedAt: time.Unix(10, 0), StaleAt: time.Unix(100, 0)},
		PriceAvailable: true,
		Capabilities: Capabilities{
			Protocols:  map[Protocol]bool{ProtocolChatCompletions: true, ProtocolResponses: true, ProtocolAnthropic: true},
			Parameters: map[string]bool{"temperature": true, "tools": true}, Tools: true, StructuredOutput: true, MaxContext: 128000, MaxOutput: 8192,
		},
		Health: HealthHealthy, Trusted: true, SuccessRateBPS: 9900, LatencyMillisP50: 100,
	}
}

func TestMatchSelectsCheapestIndependentOfInputOrder(t *testing.T) {
	engine := New()
	input := MatchInput{Request: MatchRequest{Protocol: ProtocolChatCompletions, LogicalModel: "model-a", InputTokens: 100, ExpectedOutput: 10}, Now: time.Unix(20, 0)}
	first := engine.Match(MatchInput{Request: input.Request, Routes: []Route{testRoute("expensive", "openrouter", 10, 20), testRoute("cheap", "surplus", 1, 2)}, Now: input.Now})
	second := engine.Match(MatchInput{Request: input.Request, Routes: []Route{testRoute("cheap", "surplus", 1, 2), testRoute("expensive", "openrouter", 10, 20)}, Now: input.Now})
	if first.Selected == nil || second.Selected == nil || first.Selected.Route.ID != "cheap" || second.Selected.Route.ID != "cheap" {
		t.Fatalf("expected cheap route, got %#v and %#v", first.Selected, second.Selected)
	}
	if first.Selected.ExpectedCost != 120 || second.Selected.ExpectedCost != 120 {
		t.Fatalf("unexpected expected cost: %d, %d", first.Selected.ExpectedCost, second.Selected.ExpectedCost)
	}
}

func TestMatchPrefersFreeRouteAndAcceptsFreeVariantRequests(t *testing.T) {
	free := testRoute("free", "openrouter", 100, 100)
	free.Free = true
	paid := testRoute("paid", "surplus", 1, 1)
	result := New().Match(MatchInput{Request: MatchRequest{Protocol: ProtocolChatCompletions, LogicalModel: "model-a:free", InputTokens: 1, ExpectedOutput: 1}, Routes: []Route{paid, free}, Now: time.Unix(20, 0)})
	if result.Selected == nil || result.Selected.Route.ID != "free" {
		t.Fatalf("expected free route first, got %#v", result.Selected)
	}
}

func TestMatchRejectsIncompatibleRoutesWithReasons(t *testing.T) {
	route := testRoute("r1", "openrouter", 1, 1)
	route.Capabilities.Protocols = map[Protocol]bool{ProtocolChatCompletions: true}
	route.Health = HealthBackoff
	result := New().Match(MatchInput{Request: MatchRequest{Protocol: ProtocolResponses, LogicalModel: "model-a", RequireTools: true}, Routes: []Route{route}, Now: time.Unix(20, 0)})
	if result.Selected != nil || result.Error == nil || result.Error.Code != "no_eligible_route" {
		t.Fatalf("expected no route, got %#v", result)
	}
	if len(result.Rejections) != 1 || result.Rejections[0].Code != "unsupported_protocol" {
		t.Fatalf("expected protocol rejection first, got %#v", result.Rejections)
	}
}

func TestMatchFiltersProvidersAndStalePrices(t *testing.T) {
	stale := testRoute("stale", "openrouter", 1, 1)
	allowed := testRoute("allowed", "surplus", 2, 2)
	allowed.Price.StaleAt = time.Unix(300, 0)
	result := New().Match(MatchInput{Request: MatchRequest{Protocol: ProtocolChatCompletions, LogicalModel: "model-a", AllowedProviders: []string{"SURPLUS"}}, Routes: []Route{stale, allowed}, Now: time.Unix(200, 0)})
	if result.Selected == nil || result.Selected.Route.ID != "allowed" || len(result.Rejections) != 1 || result.Rejections[0].Code != "provider_not_allowed" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestMatchTieBreaksByFreshnessSuccessLatencyAndID(t *testing.T) {
	a := testRoute("b", "p", 1, 1)
	b := testRoute("a", "p", 1, 1)
	b.Price.ObservedAt = time.Unix(20, 0)
	a.Price.ObservedAt = time.Unix(20, 0)
	a.SuccessRateBPS = 9000
	b.SuccessRateBPS = 9900
	result := New().Match(MatchInput{Request: MatchRequest{Protocol: ProtocolChatCompletions, LogicalModel: "model-a", InputTokens: 1, ExpectedOutput: 1}, Routes: []Route{a, b}, Now: time.Unix(20, 0)})
	if result.Selected == nil || result.Selected.Route.ID != "a" {
		t.Fatalf("expected stable ID tie-break after success comparison setup, got %#v", result.Selected)
	}
	a.SuccessRateBPS = b.SuccessRateBPS
	result = New().Match(MatchInput{Request: MatchRequest{Protocol: ProtocolChatCompletions, LogicalModel: "model-a", InputTokens: 1, ExpectedOutput: 1}, Routes: []Route{a, b}, Now: time.Unix(20, 0)})
	if result.Selected == nil || result.Selected.Route.ID != "a" {
		t.Fatalf("expected ID tie-break, got %#v", result.Selected)
	}
}

func TestMatchMaximumCostAndInvalidInputs(t *testing.T) {
	limit := int64(10)
	result := New().Match(MatchInput{Request: MatchRequest{Protocol: ProtocolChatCompletions, LogicalModel: "model-a", InputTokens: 5, ExpectedOutput: 5, MaximumCostPicoUSD: &limit}, Routes: []Route{testRoute("r", "p", 2, 2)}, Now: time.Unix(20, 0)})
	if result.Selected != nil || len(result.Rejections) != 1 || result.Rejections[0].Code != "over_maximum_cost" {
		t.Fatalf("expected cost rejection, got %#v", result)
	}
	invalid := New().Match(MatchInput{Request: MatchRequest{InputTokens: -1}})
	if invalid.Error == nil || invalid.Error.Code != "invalid_estimate" {
		t.Fatalf("expected invalid estimate, got %#v", invalid.Error)
	}
}

func TestEstimateUsageCostUsesCacheAndReasoningPrices(t *testing.T) {
	price := Price{InputPicoUSDPerToken: 10, OutputPicoUSDPerToken: 20, CachedReadPicoUSDPerToken: 2, CacheWritePicoUSDPerToken: 5, ReasoningPicoUSDPerToken: 7, FixedPicoUSD: 3}
	cost, err := EstimateUsageCost(UsageCostInput{InputTokens: 100, OutputTokens: 10, CachedReadTokens: 25, CacheWriteTokens: 4, ReasoningTokens: 2}, price)
	if err != nil {
		t.Fatal(err)
	}
	// 75*10 + 25*2 + 4*5 + 10*20 + 2*7 + 3.
	if cost != 1037 {
		t.Fatalf("got cost %d", cost)
	}
	cost, err = EstimateUsageCost(UsageCostInput{InputTokens: 75, OutputTokens: 10, CachedReadTokens: 25, InputTokensNetOfCache: true}, price)
	if err != nil || cost != 1003 {
		t.Fatalf("net-cache cost: %d, %v", cost, err)
	}
}

func TestMatchAllowsExplicitStaleAndUntrustedRoutes(t *testing.T) {
	route := testRoute("r", "p", 1, 1)
	route.Trusted = false
	result := New().Match(MatchInput{Request: MatchRequest{Protocol: ProtocolChatCompletions, LogicalModel: "model-a", AllowStale: true, AllowUntrusted: true}, Routes: []Route{route}, Now: time.Unix(200, 0)})
	if result.Selected == nil {
		t.Fatalf("expected explicitly allowed route, got %#v", result)
	}
}

func TestMatchRejectsMissingRequiredModalities(t *testing.T) {
	route := testRoute("r", "p", 1, 1)
	route.Capabilities.InputModalities = map[string]bool{"text": true}
	route.Capabilities.OutputModalities = map[string]bool{"text": true}
	result := New().Match(MatchInput{Request: MatchRequest{Protocol: ProtocolChatCompletions, LogicalModel: "model-a", RequiredInputModalities: []string{"audio"}}, Routes: []Route{route}, Now: time.Unix(20, 0)})
	if result.Selected != nil || len(result.Rejections) != 1 || result.Rejections[0].Code != "missing_modality" {
		t.Fatalf("unexpected modality result: %#v", result)
	}
}

func TestMatchFiltersModelSetBillingAndPerTokenCaps(t *testing.T) {
	cheap := testRoute("cheap", "model-a", 2, 2)
	cheap.BillingClass = BillingMetered
	expensive := testRoute("expensive", "model-b", 3, 3)
	expensive.BillingClass = BillingSubscription
	inputCap := int64(2)
	result := New().Match(MatchInput{Request: MatchRequest{Protocol: ProtocolChatCompletions, LogicalModels: []string{"model-a", "model-b"}, InputTokens: 1, ExpectedOutput: 1, AllowedBillingClasses: []BillingClass{BillingMetered}, MaximumInputPicoUSDPerToken: &inputCap}, Routes: []Route{expensive, cheap}, Now: time.Unix(20, 0)})
	if result.Selected == nil || result.Selected.Route.ID != "cheap" {
		t.Fatalf("expected cheap metered route, got %#v", result)
	}
	if len(result.Rejections) != 1 || result.Rejections[0].Code != "wrong_billing_class" {
		t.Fatalf("unexpected rejection: %#v", result.Rejections)
	}
}

func TestMatchSurplusOfficialPricePercentage(t *testing.T) {
	route := testRoute("auction", "surplus", 30, 40)
	route.OfficialPriceAvailable = true
	route.OfficialPrice = Price{InputPicoUSDPerToken: 100, OutputPicoUSDPerToken: 100}
	percent := 50
	result := New().Match(MatchInput{Request: MatchRequest{Protocol: ProtocolChatCompletions, LogicalModel: "model-a", MaximumOfficialPricePercent: &percent}, Routes: []Route{route}, Now: time.Unix(20, 0)})
	if result.Selected == nil {
		t.Fatalf("expected route under 50%% cap, got %#v", result)
	}
	route.Price.InputPicoUSDPerToken = 51
	result = New().Match(MatchInput{Request: MatchRequest{Protocol: ProtocolChatCompletions, LogicalModel: "model-a", MaximumOfficialPricePercent: &percent}, Routes: []Route{route}, Now: time.Unix(20, 0)})
	if result.Selected != nil || len(result.Rejections) != 1 || result.Rejections[0].Code != "over_official_price_limit" {
		t.Fatalf("expected auction rejection, got %#v", result)
	}
	route.OfficialPriceAvailable = false
	result = New().Match(MatchInput{Request: MatchRequest{Protocol: ProtocolChatCompletions, LogicalModel: "model-a", MaximumOfficialPricePercent: &percent}, Routes: []Route{route}, Now: time.Unix(20, 0)})
	if result.Selected != nil || result.Rejections[0].Code != "missing_official_price" {
		t.Fatalf("expected missing official price, got %#v", result)
	}
}

func TestMatchOpenRouterOfficialPricePercentage(t *testing.T) {
	route := testRoute("openrouter", "openrouter", 30, 40)
	route.OfficialPriceAvailable = true
	route.OfficialPrice = Price{InputPicoUSDPerToken: 100, OutputPicoUSDPerToken: 100}
	percent := 50
	result := New().Match(MatchInput{Request: MatchRequest{Protocol: ProtocolChatCompletions, LogicalModel: "model-a", MaximumOfficialPricePercent: &percent}, Routes: []Route{route}, Now: time.Unix(20, 0)})
	if result.Selected == nil {
		t.Fatalf("expected OpenRouter route under 50%% cap, got %#v", result)
	}
	route.Price.InputPicoUSDPerToken = 51
	result = New().Match(MatchInput{Request: MatchRequest{Protocol: ProtocolChatCompletions, LogicalModel: "model-a", MaximumOfficialPricePercent: &percent}, Routes: []Route{route}, Now: time.Unix(20, 0)})
	if result.Selected != nil || len(result.Rejections) != 1 || result.Rejections[0].Code != "over_official_price_limit" {
		t.Fatalf("expected OpenRouter rejection, got %#v", result)
	}
}
