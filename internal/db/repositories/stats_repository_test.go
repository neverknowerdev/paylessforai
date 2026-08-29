package repositories_test

import (
	"testing"

	"github.com/neverknowerdev/paylessforai/internal/db/models"
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
