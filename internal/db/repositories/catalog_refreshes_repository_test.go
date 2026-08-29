package repositories_test

import (
	"testing"
	"time"

	bobmodels "github.com/neverknowerdev/paylessforai/internal/db/bob/models"
	"github.com/neverknowerdev/paylessforai/internal/db/models"
	"github.com/stephenafamo/bob"
)

func TestCatalogRefreshesRepositoryIntegration(t *testing.T) {
	i := newIntegrationDB(t)
	i.reset(t)
	refresh := models.CatalogRefresh{ID: "refresh-1", Provider: "provider", Status: "complete", StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if err := i.repos.CatalogRefreshes.Create(i.ctx, refresh); err != nil {
		t.Fatal(err)
	}
	row, err := bobmodels.FindCatalogRefresh(i.ctx, bob.NewDB(i.db), refresh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row.Provider != refresh.Provider {
		t.Fatalf("provider: %q", row.Provider)
	}
}
