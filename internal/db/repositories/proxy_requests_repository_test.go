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
	planJSON := `{"group_id":"group-1","selected":"model"}`
	if err := i.repos.ProxyRequests.RecordResolution(i.ctx, "request-1", "group-1", 3, planJSON, "model"); err != nil {
		t.Fatal(err)
	}
	if err := i.repos.ProxyRequests.Complete(i.ctx, "request-1", "failed", "provider_quota_exhausted", "quota"); err != nil {
		t.Fatal(err)
	}
	row, err := bobmodels.FindProxyRequest(i.ctx, bob.NewDB(i.db), "request-1")
	if err != nil {
		t.Fatal(err)
	}
	if row.SelectedProvider.V != "provider" || row.StatsDisposition != "excluded_limit" || row.State != "failed" || row.AttemptCount != 1 {
		t.Fatalf("request row: provider=%q disposition=%q state=%q attempts=%d", row.SelectedProvider.V, row.StatsDisposition, row.State, row.AttemptCount)
	}
	if row.ResolvedGroupID.V != "group-1" || row.ResolvedGroupRevision.V != 3 || row.ResolvedPlanJSON.V != planJSON || row.SelectedLogicalModel.V != "model" {
		t.Fatalf("resolution row: group=%q revision=%d plan=%q selected=%q", row.ResolvedGroupID.V, row.ResolvedGroupRevision.V, row.ResolvedPlanJSON.V, row.SelectedLogicalModel.V)
	}
}
