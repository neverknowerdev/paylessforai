package repositories_test

import (
	"testing"
	"time"

	"github.com/neverknowerdev/paylessforai/internal/db/models"
)

func TestCatalogRefreshesRepositoryIntegration(t *testing.T) {
	i := newIntegrationDB(t)
	i.reset(t)
	refresh := models.CatalogRefresh{ID: "refresh-1", Provider: "provider", Status: "complete", StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if err := i.repos.CatalogRefreshes.Create(i.ctx, refresh); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := i.db.QueryRowContext(i.ctx, `SELECT provider FROM catalog_refreshes WHERE id = $1`, refresh.ID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != refresh.Provider {
		t.Fatalf("provider: %q", got)
	}
}
