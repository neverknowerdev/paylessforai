package repositories_test

import (
	"testing"

	bobmodels "github.com/neverknowerdev/paylessforai/internal/db/bob/models"
	"github.com/stephenafamo/bob"
)

func TestProxyAttemptsRepositoryIntegration(t *testing.T) {
	i := newIntegrationDB(t)
	i.reset(t)
	if err := i.repos.ProxyRequests.Create(i.ctx, "request-1", "", "chat.completions", "model"); err != nil {
		t.Fatal(err)
	}
	if err := i.repos.ProxyAttempts.Record(i.ctx, "request-1", 1, "provider", "model", "started", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := i.repos.ProxyAttempts.Record(i.ctx, "request-1", 1, "provider", "model", "failed", "quota_exhausted", "quota"); err != nil {
		t.Fatal(err)
	}
	row, err := bobmodels.FindProxyAttempt(i.ctx, bob.NewDB(i.db), "request-1:1")
	if err != nil {
		t.Fatal(err)
	}
	if row.State != "failed" || row.StatsDisposition != "excluded_limit" || !row.CompletedAt.Valid {
		t.Fatalf("attempt row: state=%q disposition=%q completed_at=%v", row.State, row.StatsDisposition, row.CompletedAt)
	}
}
