package repositories_test

import (
	"testing"
	"time"

	"github.com/neverknowerdev/paylessforai/internal/db/models"
)

func TestCatalogRefreshesRepositoryIntegration(t *testing.T) {
	i := newIntegrationDB(t)
	refresh := models.CatalogRefresh{ID: "refresh-1", Provider: "provider", Status: "complete", StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if err := i.repos.CatalogRefreshes.Create(i.ctx, refresh); err != nil {
		t.Fatal(err)
	}
	if err := i.repos.CatalogRefreshes.Create(i.ctx, refresh); err == nil {
		t.Fatal("expected duplicate refresh to be rejected")
	}
}
