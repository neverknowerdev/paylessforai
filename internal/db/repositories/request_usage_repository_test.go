package repositories_test

import (
	"testing"

	"github.com/neverknowerdev/paylessforai/internal/db/models"
)

func TestRequestUsageRepositoryIntegration(t *testing.T) {
	i := newIntegrationDB(t)
	if err := i.repos.ProxyRequests.Create(i.ctx, "request-1", "", "chat.completions", "model"); err != nil {
		t.Fatal(err)
	}
	usage := models.RequestUsage{RequestID: "request-1", InputTokens: 10, OutputTokens: 5, TotalTokens: 15, RawUsageJSON: "{}"}
	if err := i.repos.RequestUsage.Upsert(i.ctx, usage); err != nil {
		t.Fatal(err)
	}
	stats, err := i.repos.Stats.ListRequestStats(i.ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 || stats[0].ID != usage.RequestID || stats[0].TotalTokens != 15 {
		t.Fatalf("usage stats: %#v", stats)
	}
}
