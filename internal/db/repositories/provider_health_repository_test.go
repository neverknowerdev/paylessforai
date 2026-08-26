package repositories_test

import (
	"testing"
	"time"

	"github.com/neverknowerdev/paylessforai/internal/db/models"
)

func TestProviderHealthRepositoryIntegration(t *testing.T) {
	i := newIntegrationDB(t)
	i.reset(t)
	health := models.ProviderHealthRecord{RouteID: "route-1", FailureCount: 2, State: "healthy", UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if err := i.repos.ProviderHealth.Upsert(i.ctx, health); err != nil {
		t.Fatal(err)
	}
	if got, err := i.repos.ProviderHealth.Get(i.ctx, health.RouteID); err != nil || got.FailureCount != health.FailureCount {
		t.Fatalf("get: %+v, %v", got, err)
	}
	if err := i.repos.ProviderHealth.DeleteAll(i.ctx); err != nil {
		t.Fatal(err)
	}
}
