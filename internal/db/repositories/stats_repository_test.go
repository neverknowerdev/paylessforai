package repositories_test

import (
	"testing"

	"github.com/neverknowerdev/paylessforai/internal/db/models"
	"github.com/neverknowerdev/paylessforai/internal/groups"
)

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
