package routing

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/neverknowerdev/paylessforai/internal/groups"
	"github.com/neverknowerdev/paylessforai/internal/matcher"
)

func route(id, model string, input, output int64, billing matcher.BillingClass) matcher.Route {
	return matcher.Route{ID: id, Provider: "p", LogicalModel: model, UpstreamModel: model, Price: matcher.Price{InputPicoUSDPerToken: input, OutputPicoUSDPerToken: output, ObservedAt: time.Unix(1, 0), StaleAt: time.Unix(100, 0)}, PriceAvailable: true, BillingClass: billing, Health: matcher.HealthHealthy, Trusted: true, Capabilities: matcher.Capabilities{Protocols: map[matcher.Protocol]bool{matcher.ProtocolChatCompletions: true}, MaxContext: 100000, MaxOutput: 4096}}
}

func TestBuildGroupPreservesStageOrderAndRetryBudget(t *testing.T) {
	definition := groups.Definition{ID: "g", Slug: "g", Enabled: true, Revision: 4, Stages: []groups.Stage{
		{Position: 0, Name: "free", Sources: []groups.Source{{Kind: groups.SourceModel, ModelID: "model-a"}}, BillingClasses: []groups.BillingClass{groups.BillingFree}, SameRouteRetries: intPtr(3)},
		{Position: 1, Name: "paid", Sources: []groups.Source{{Kind: groups.SourceModel, ModelID: "model-a"}}, BillingClasses: []groups.BillingClass{groups.BillingMetered}},
	}}
	routes := []matcher.Route{route("paid", "model-a", 1, 1, matcher.BillingMetered), route("free", "model-a", 9, 9, matcher.BillingFree)}
	plan := BuildGroup(matcher.MatchRequest{Protocol: matcher.ProtocolChatCompletions, LogicalModel: "g", InputTokens: 1, ExpectedOutput: 1}, definition, map[string]groups.Definition{"g": definition}, routes, time.Unix(2, 0), DefaultLimits())
	if plan.Error != nil || len(plan.Entries) != 2 || plan.Entries[0].Route.ID != "free" || plan.Entries[0].SameRouteRetries != 3 || plan.Entries[1].Route.ID != "paid" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
	if plan.MaxAttempts() != 5 {
		t.Fatalf("expected 5 projected attempts, got %d", plan.MaxAttempts())
	}
}

func TestBuildGroupReportsPriceLimit(t *testing.T) {
	definition := groups.Definition{ID: "g", Slug: "g", Enabled: true, Stages: []groups.Stage{{Position: 0, Sources: []groups.Source{{Kind: groups.SourceModel, ModelID: "model-a"}}, BillingClasses: []groups.BillingClass{groups.BillingMetered}, MaximumInputPicoUSDPerToken: int64Ptr(1)}}}
	routes := []matcher.Route{route("free", "model-a", 0, 0, matcher.BillingFree), route("paid", "model-a", 2, 1, matcher.BillingMetered)}
	plan := BuildGroup(matcher.MatchRequest{Protocol: matcher.ProtocolChatCompletions, LogicalModel: "g", InputTokens: 1, ExpectedOutput: 1}, definition, map[string]groups.Definition{"g": definition}, routes, time.Unix(2, 0), DefaultLimits())
	if plan.Error == nil || plan.Error.Code != "group_price_limit_exceeded" {
		t.Fatalf("expected price error, got %#v", plan.Error)
	}
}

func TestBuildGroupPinsAllProviderSourceWhenFutureProvidersExcluded(t *testing.T) {
	definition := groups.Definition{ID: "g", Slug: "g", Enabled: true, Stages: []groups.Stage{{Position: 0, Sources: []groups.Source{{Kind: groups.SourceModel, ModelID: "model-a", ProviderNames: []string{"p1"}, IncludeNewProviders: false}}, BillingClasses: []groups.BillingClass{groups.BillingMetered}}}}
	pinned := route("pinned", "model-a", 1, 1, matcher.BillingMetered)
	pinned.Provider = "p1"
	future := route("future", "model-a", 2, 2, matcher.BillingMetered)
	future.Provider = "p2"
	plan := BuildGroup(matcher.MatchRequest{Protocol: matcher.ProtocolChatCompletions, LogicalModel: "g", InputTokens: 1, ExpectedOutput: 1}, definition, map[string]groups.Definition{"g": definition}, []matcher.Route{future, pinned}, time.Unix(2, 0), DefaultLimits())
	if plan.Error != nil || len(plan.Entries) != 1 || plan.Entries[0].Route.ID != "pinned" {
		t.Fatalf("expected only pinned provider route, got %#v", plan)
	}
}

func TestBuildGroupModelWideSourceExcludesSubscriptionRoute(t *testing.T) {
	definition := groups.Definition{ID: "g", Slug: "g", Enabled: true, Stages: []groups.Stage{{Position: 0, Sources: []groups.Source{{Kind: groups.SourceModel, ModelID: "model-a"}}, BillingClasses: groups.AllBillingClasses}}}
	subscription := route("subscription", "model-a", 1, 1, matcher.BillingSubscription)
	subscription.Provider = "subscription-provider"
	metered := route("metered", "model-a", 2, 2, matcher.BillingMetered)
	metered.Provider = "metered-provider"
	plan := BuildGroup(matcher.MatchRequest{Protocol: matcher.ProtocolChatCompletions, LogicalModel: "g", InputTokens: 1, ExpectedOutput: 1}, definition, map[string]groups.Definition{"g": definition}, []matcher.Route{subscription, metered}, time.Unix(2, 0), DefaultLimits())
	if plan.Error != nil || len(plan.Entries) != 1 || plan.Entries[0].Route.ID != "metered" {
		t.Fatalf("model-wide source must exclude subscription route, got %#v", plan)
	}
}

func TestBuildGroupExplicitSubscriptionRouteRemainsAvailable(t *testing.T) {
	definition := groups.Definition{ID: "g", Slug: "g", Enabled: true, Stages: []groups.Stage{{Position: 0, Sources: []groups.Source{{Kind: groups.SourceModel, ModelID: "model-a", ProviderName: "subscription-provider"}}, BillingClasses: []groups.BillingClass{groups.BillingSubscription}}}}
	subscription := route("subscription", "model-a", 1, 1, matcher.BillingSubscription)
	subscription.Provider = "subscription-provider"
	plan := BuildGroup(matcher.MatchRequest{Protocol: matcher.ProtocolChatCompletions, LogicalModel: "g", InputTokens: 1, ExpectedOutput: 1}, definition, map[string]groups.Definition{"g": definition}, []matcher.Route{subscription}, time.Unix(2, 0), DefaultLimits())
	if plan.Error != nil || len(plan.Entries) != 1 || plan.Entries[0].Route.ID != "subscription" {
		t.Fatalf("explicit subscription route should remain available, got %#v", plan)
	}
}

func TestBuildGroupRepeatsTryBlockAndKeepsSourceOrder(t *testing.T) {
	sourceRetries := 0
	definition := groups.Definition{ID: "g", Slug: "g", Enabled: true, Stages: []groups.Stage{{Position: 0, Name: "auction", TryRetries: intPtr(1), Sources: []groups.Source{{Kind: groups.SourceModel, ModelID: "model-a", Retries: &sourceRetries}, {Kind: groups.SourceModel, ModelID: "model-b", Retries: &sourceRetries}}, BillingClasses: []groups.BillingClass{groups.BillingMetered}}}}
	routes := []matcher.Route{route("a", "model-a", 1, 1, matcher.BillingMetered), route("b", "model-b", 2, 2, matcher.BillingMetered)}
	plan := BuildGroup(matcher.MatchRequest{Protocol: matcher.ProtocolChatCompletions, LogicalModel: "g", InputTokens: 1, ExpectedOutput: 1}, definition, map[string]groups.Definition{"g": definition}, routes, time.Unix(2, 0), DefaultLimits())
	if plan.Error != nil || len(plan.Entries) != 4 || plan.Entries[0].Route.ID != "a" || plan.Entries[1].Route.ID != "b" || plan.Entries[2].Route.ID != "a" || plan.Entries[3].Route.ID != "b" {
		t.Fatalf("expected repeated ordered try block, got %#v", plan)
	}
}

func TestPlanJSONUsesStableLowercaseFields(t *testing.T) {
	definition := groups.Definition{ID: "g", Slug: "g", Enabled: true, Stages: []groups.Stage{{Position: 0, Sources: []groups.Source{{Kind: groups.SourceModel, ModelID: "model-a"}}, BillingClasses: []groups.BillingClass{groups.BillingFree}}}}
	plan := BuildGroup(matcher.MatchRequest{Protocol: matcher.ProtocolChatCompletions, LogicalModel: "g"}, definition, map[string]groups.Definition{"g": definition}, []matcher.Route{route("free", "model-a", 0, 0, matcher.BillingFree)}, time.Unix(2, 0), DefaultLimits())
	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "" || !containsJSONField(data, "entries") || containsJSONField(data, "Entries") {
		t.Fatalf("unexpected plan JSON: %s", data)
	}
}

func containsJSONField(data []byte, field string) bool {
	needle := []byte(`"` + field + `"`)
	return string(data) != "" && string(data) != "null" && bytes.Contains(data, needle)
}

func intPtr(value int) *int       { return &value }
func int64Ptr(value int64) *int64 { return &value }
