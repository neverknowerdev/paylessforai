package repositories_test

import (
	"testing"

	bobmodels "github.com/neverknowerdev/paylessforai/internal/db/bob/models"
	"github.com/stephenafamo/bob"
)

func TestProxyRequestsRepositoryIntegration(t *testing.T) {
	i := newIntegrationDB(t)
	i.reset(t)
	if err := i.repos.ProxyRequests.Create(i.ctx, "request-1", "", "chat.completions", "model"); err != nil {
		t.Fatal(err)
	}
	if err := i.repos.ProxyRequests.RecordAttemptRoute(i.ctx, "request-1", 1, "provider", "model"); err != nil {
		t.Fatal(err)
	}
	if err := i.repos.ProxyRequests.Complete(i.ctx, "request-1", "failed", "provider_quota_exhausted", "quota"); err != nil {
		t.Fatal(err)
	}
	row, err := bobmodels.FindProxyRequest(i.ctx, bob.NewDB(i.db), "request-1")
	if err != nil {
		t.Fatal(err)
	}
	if row.SelectedProvider.V != "provider" || !row.SelectedProvider.Valid || row.StatsDisposition != "excluded_limit" || row.State != "failed" || row.AttemptCount != 1 {
		t.Fatalf("request row: %+v", row)
	}
}
