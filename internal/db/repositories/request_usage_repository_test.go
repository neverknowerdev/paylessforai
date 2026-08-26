package repositories_test

import (
	"testing"

	"github.com/neverknowerdev/paylessforai/internal/db/models"
)

func TestRequestUsageRepositoryIntegration(t *testing.T) {
	i := newIntegrationDB(t)
	i.reset(t)
	if err := i.repos.ProxyRequests.Create(i.ctx, "request-1", "", "chat.completions", "model"); err != nil {
		t.Fatal(err)
	}
	usage := models.RequestUsage{RequestID: "request-1", InputTokens: 10, OutputTokens: 5, TotalTokens: 15, RawUsageJSON: "{}"}
	if err := i.repos.RequestUsage.Upsert(i.ctx, usage); err != nil {
		t.Fatal(err)
	}
	var total int64
	if err := i.db.QueryRowContext(i.ctx, `SELECT total_tokens FROM request_usage WHERE request_id = $1`, usage.RequestID).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 15 {
		t.Fatalf("usage total: %d", total)
	}
}
