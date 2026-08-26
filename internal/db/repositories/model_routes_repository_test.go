package repositories_test

import (
	"testing"
	"time"

	"github.com/neverknowerdev/paylessforai/internal/db/models"
)

func TestModelRoutesRepositoryIntegration(t *testing.T) {
	i := newIntegrationDB(t)
	i.reset(t)
	if err := i.repos.Models.Upsert(i.ctx, models.ModelRecord{ID: "model-1", DisplayName: "Model", ObservedAt: time.Now().UTC().Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}
	route := models.ModelRouteRecord{ID: "route-1", ModelID: "model-1", Provider: "provider", UpstreamModel: "model", Protocol: "chat.completions", PriceJSON: "{}", CapabilitiesJSON: "{}", Health: "healthy", ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), Trusted: true}
	if err := i.repos.ModelRoutes.Upsert(i.ctx, route); err != nil {
		t.Fatal(err)
	}
	if got, err := i.repos.ModelRoutes.Get(i.ctx, route.ID); err != nil || !got.Trusted {
		t.Fatalf("get: %+v, %v", got, err)
	}
	if err := i.repos.ModelRoutes.DeleteAll(i.ctx); err != nil {
		t.Fatal(err)
	}
}
