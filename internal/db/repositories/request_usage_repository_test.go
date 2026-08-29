package repositories_test

import (
	"testing"

	bobmodels "github.com/neverknowerdev/paylessforai/internal/db/bob/models"
	"github.com/neverknowerdev/paylessforai/internal/db/models"
	"github.com/stephenafamo/bob"
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
	row, err := bobmodels.FindRequestUsage(i.ctx, bob.NewDB(i.db), usage.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	if row.TotalTokens != 15 {
		t.Fatalf("usage total: %d", row.TotalTokens)
	}
}
