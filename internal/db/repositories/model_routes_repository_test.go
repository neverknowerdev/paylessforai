package repositories_test

import (
	"testing"
	"time"

	"github.com/neverknowerdev/paylessforai/internal/db/models"
	"github.com/neverknowerdev/paylessforai/internal/wire"
)

func TestModelRoutesRepositoryIntegration(t *testing.T) {
	i := newIntegrationDB(t)
	if err := i.repos.Models.Upsert(i.ctx, models.ModelRecord{ID: "model-1", DisplayName: "Model", ObservedAt: time.Now().UTC().Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}
	route := models.ModelRouteRecord{ID: "route-1", ModelID: "model-1", Provider: "provider", UpstreamModel: "model", Format: string(wire.FormatResponses), PriceJSON: "{}", CapabilitiesJSON: "{}", Health: "healthy", ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), Trusted: true}
	if err := i.repos.ModelRoutes.Upsert(i.ctx, route); err != nil {
		t.Fatal(err)
	}
	if got, err := i.repos.ModelRoutes.Get(i.ctx, route.ID); err != nil || !got.Trusted {
		t.Fatalf("get: %+v, %v", got, err)
	}
	if got, ok, err := i.repos.ModelRoutes.GetFormat(i.ctx, route.ID); err != nil || !ok || got != wire.FormatResponses {
		t.Fatalf("learned format was not loaded: %v %v %v", got, ok, err)
	}
	if supported, known, err := i.repos.ModelRoutes.GetStructuredOutputCapability(i.ctx, route.ID); err != nil || known || supported {
		t.Fatalf("structured output should start unknown: %v %v %v", supported, known, err)
	}
	if err := i.repos.ModelRoutes.SetStructuredOutputCapability(i.ctx, route.ID, true); err != nil {
		t.Fatal(err)
	}
	if supported, known, err := i.repos.ModelRoutes.GetStructuredOutputCapability(i.ctx, route.ID); err != nil || !known || !supported {
		t.Fatalf("structured output result was not persisted: %v %v %v", supported, known, err)
	}
	if err := i.repos.ModelRoutes.SetStructuredOutputCapability(i.ctx, route.ID, false); err != nil {
		t.Fatal(err)
	}
	if supported, known, err := i.repos.ModelRoutes.GetStructuredOutputCapability(i.ctx, route.ID); err != nil || !known || supported {
		t.Fatalf("negative structured output result was not persisted: %v %v %v", supported, known, err)
	}
	if err := i.repos.ModelRoutes.ClearFormat(i.ctx, route.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := i.repos.ModelRoutes.GetFormat(i.ctx, route.ID); err != nil || ok {
		t.Fatalf("format was not cleared: %v %v", ok, err)
	}
	if err := i.repos.ModelRoutes.DeleteAll(i.ctx); err != nil {
		t.Fatal(err)
	}
}
