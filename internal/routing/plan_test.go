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
