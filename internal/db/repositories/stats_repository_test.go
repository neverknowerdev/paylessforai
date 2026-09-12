package repositories_test

import (
	"testing"
	"time"

	"github.com/neverknowerdev/paylessforai/internal/db/models"
	"github.com/neverknowerdev/paylessforai/internal/groups"
)

func TestStatsRepositoryRouteUsageSinceCountsFinalSelectedRoutes(t *testing.T) {
	i := newIntegrationDB(t)
	for _, request := range []struct {
		id, provider, upstream string
	}{
		{id: "route-a-1", provider: "surplus", upstream: "model-a"},
		{id: "route-a-2", provider: "surplus", upstream: "model-a"},
		{id: "route-b-1", provider: "openrouter", upstream: "model-a"},
		{id: "unrouted"},
	} {
		if err := i.repos.ProxyRequests.Create(i.ctx, request.id, "", "chat.completions", "model-a"); err != nil {
			t.Fatal(err)
		}
		if request.provider != "" {
			if err := i.repos.ProxyRequests.RecordAttemptRoute(i.ctx, request.id, 1, request.provider, request.upstream); err != nil {
				t.Fatal(err)
			}
		}
	}
	old := time.Now().UTC().Add(-8 * 24 * time.Hour).Format(time.RFC3339Nano)
	if _, err := i.repos.DB().ExecContext(i.ctx, `UPDATE proxy_requests SET received_at = ? WHERE id = ?`, old, "route-a-2"); err != nil {
		t.Fatal(err)
	}
	usage, err := i.repos.Stats.RouteUsageSince(i.ctx, time.Now().UTC().Add(-7*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if usage["surplus\x00model-a"] != 1 || usage["openrouter\x00model-a"] != 1 || len(usage) != 2 {
		t.Fatalf("unexpected route usage: %#v", usage)
	}
}

func TestStatsRepositoryIntegrationAggregatesBobRows(t *testing.T) {
	i := newIntegrationDB(t)
	if err := i.repos.ProxyRequests.Create(i.ctx, "stats-request", "", "chat.completions", "model"); err != nil {
		t.Fatal(err)
	}
	if err := i.repos.ProxyRequests.Complete(i.ctx, "stats-request", "succeeded", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := i.repos.RequestUsage.Upsert(i.ctx, models.RequestUsage{RequestID: "stats-request", InputTokens: 3, OutputTokens: 2, TotalTokens: 5, RawUsageJSON: "{}"}); err != nil {
		t.Fatal(err)
	}
	summary, err := i.repos.Stats.RequestStatsSummary(i.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if summary.TotalRequests != 1 || summary.SucceededRequests != 1 || summary.InputTokens != 3 || summary.OutputTokens != 2 || summary.TotalTokens != 5 {
		t.Fatalf("unexpected stats summary: %#v", summary)
	}
}

func TestStatsRepositoryGroupStatsAggregatesResolvedGroup(t *testing.T) {
	i := newIntegrationDB(t)
	definition, err := i.repos.Groups.Save(i.ctx, groups.Definition{
		ID: "group-coding", Name: "Coding", Slug: "coding", Enabled: true,
		Stages: []groups.Stage{{Name: "primary", Sources: []groups.Source{{Kind: groups.SourceModel, ModelID: "model"}}}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := i.repos.ProxyRequests.Create(i.ctx, "group-stats-request", "", "chat.completions", "coding"); err != nil {
		t.Fatal(err)
	}
	if err := i.repos.ProxyRequests.RecordResolution(i.ctx, "group-stats-request", definition.ID, definition.Revision, "{}", "model"); err != nil {
		t.Fatal(err)
	}
	if err := i.repos.ProxyRequests.RecordAttemptRoute(i.ctx, "group-stats-request", 2, "surplus", "model"); err != nil {
		t.Fatal(err)
	}
	if err := i.repos.ProxyRequests.Complete(i.ctx, "group-stats-request", "succeeded", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := i.repos.RequestUsage.Upsert(i.ctx, models.RequestUsage{RequestID: "group-stats-request", InputTokens: 4, OutputTokens: 3, TotalTokens: 7, EstimatedCostPico: 40, OfficialCostPico: 100, DiscountPico: ptrInt64(60), RawUsageJSON: "{}"}); err != nil {
		t.Fatal(err)
	}
	items, err := i.repos.Stats.GroupStats(i.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].GroupID != "group-coding" || items[0].Group != "Coding" || items[0].Slug != "coding" || items[0].Requests != 1 || items[0].SucceededRequests != 1 || items[0].TotalAttempts != 2 || items[0].RetriedRequests != 1 || items[0].TotalTokens != 7 || items[0].SavedCostPico != 60 || items[0].DiscountBPS == nil || *items[0].DiscountBPS != 6000 {
		t.Fatalf("unexpected group stats: %#v", items)
	}
}

func ptrInt64(value int64) *int64 { return &value }

func TestStatsRepositoryIntegrationPreservesOverallErrorMessage(t *testing.T) {
	i := newIntegrationDB(t)
	if err := i.repos.ProxyRequests.Create(i.ctx, "failed-request", "", "chat.completions", "model"); err != nil {
		t.Fatal(err)
	}
	if err := i.repos.ProxyRequests.Complete(i.ctx, "failed-request", "failed", "model_not_found", "all provider attempts failed"); err != nil {
		t.Fatal(err)
	}
	items, err := i.repos.Stats.ListRequestStats(i.ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ErrorMessage == nil || *items[0].ErrorMessage != "all provider attempts failed" || items[0].ErrorCode == nil || *items[0].ErrorCode != "model_not_found" {
		t.Fatalf("lost overall failure or terminal classification: %#v", items)
	}
}

func TestStatsRepositoryIncludesSkippedRoutesFromResolvedPlan(t *testing.T) {
	i := newIntegrationDB(t)
	if err := i.repos.ProxyRequests.Create(i.ctx, "skipped-route-request", "", "chat.completions", "model-a"); err != nil {
		t.Fatal(err)
	}
	plan := `{"requested_model":"model-a","rejections":[{"route_id":"credential-1:model-a","provider":"opencode-go","logical_model":"model-a","upstream_model":"model-a","code":"missing_capability","detail":"route does not support structured output"}]}`
	if _, err := i.repos.DB().ExecContext(i.ctx, `UPDATE proxy_requests SET resolved_plan_json = ? WHERE id = ?`, plan, "skipped-route-request"); err != nil {
		t.Fatal(err)
	}
	items, err := i.repos.Stats.ListRequestStats(i.ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || len(items[0].SkippedRoutes) != 1 {
		t.Fatalf("expected one skipped route, got %#v", items)
	}
	skipped := items[0].SkippedRoutes[0]
	if skipped.State != "skipped" || skipped.Provider != "opencode-go" || skipped.UpstreamModel != "model-a" || skipped.ReasonCode != "missing_capability" || skipped.Reason != "route does not support structured output" {
		t.Fatalf("unexpected skipped route: %#v", skipped)
	}
}
