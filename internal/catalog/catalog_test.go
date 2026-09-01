package catalog

import (
	"context"
	"net/http"
	"testing"

	"github.com/neverknowerdev/paylessforai/internal/matcher"
	"github.com/neverknowerdev/paylessforai/internal/providers"
)

type fakeClient struct {
	name   string
	models []providers.Model
}

func (f fakeClient) Name() string                                        { return f.name }
func (f fakeClient) Discover(context.Context) ([]providers.Model, error) { return f.models, nil }
func (f fakeClient) Do(context.Context, matcher.Protocol, string, []byte) (*http.Response, error) {
	return nil, nil
}

func TestRefreshMergesOpenRouterAndSurplusAliases(t *testing.T) {
	price := matcher.Price{InputPicoUSDPerToken: 1, OutputPicoUSDPerToken: 2}
	manager := New([]providers.Client{
		fakeClient{name: "openrouter", models: []providers.Model{{ID: "anthropic/model-a", Name: "Model A", Pricing: price, PriceAvailable: true}}},
		fakeClient{name: "surplus", models: []providers.Model{{ID: "model-a", Name: "Model A", Pricing: price, PriceAvailable: true}}},
	})
	if err := manager.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	snapshot := manager.Snapshot()
	if len(snapshot.Routes) != 2 || snapshot.Routes[0].LogicalModel != "anthropic/model-a" || snapshot.Routes[1].LogicalModel != "anthropic/model-a" {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
}

func TestRefreshMergesFreeOpenRouterVariantWithPaidProvider(t *testing.T) {
	manager := New([]providers.Client{
		fakeClient{name: "openrouter", models: []providers.Model{{ID: "model-a:free", Name: "Model A", Free: true, PriceAvailable: true}}},
		fakeClient{name: "surplus", models: []providers.Model{{ID: "model-a", Name: "Model A", Pricing: matcher.Price{InputPicoUSDPerToken: 1, OutputPicoUSDPerToken: 1}, PriceAvailable: true}}},
	})
	if err := manager.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	snapshot := manager.Snapshot()
	if len(snapshot.Models) != 1 || snapshot.Models[0].ID != "model-a" || !snapshot.Models[0].Free || len(snapshot.Routes) != 2 {
		t.Fatalf("unexpected free variant snapshot: %#v", snapshot)
	}
	if !snapshot.Routes[0].Free && !snapshot.Routes[1].Free {
		t.Fatalf("expected one free route: %#v", snapshot.Routes)
	}
}

func TestRefreshDoesNotInferFreeFromZeroTokenPrice(t *testing.T) {
	manager := New([]providers.Client{fakeClient{name: "surplus", models: []providers.Model{{ID: "media-model", Name: "Media Model", PriceAvailable: true}}}})
	if err := manager.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	snapshot := manager.Snapshot()
	if len(snapshot.Routes) != 1 || snapshot.Routes[0].Free {
		t.Fatalf("zero token price must not imply a free route: %#v", snapshot.Routes)
	}
}

func TestRefreshPropagatesModalitiesAndTags(t *testing.T) {
	manager := New([]providers.Client{fakeClient{name: "surplus", models: []providers.Model{{ID: "model-a", Name: "Model A", Pricing: matcher.Price{InputPicoUSDPerToken: 1, OutputPicoUSDPerToken: 1}, PriceAvailable: true, InputModalities: []string{"text", "audio"}, OutputModalities: []string{"text"}, Tags: []string{"streaming"}}}}})
	if err := manager.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	route := manager.Snapshot().Routes[0]
	if !route.Capabilities.InputModalities["audio"] || !route.Capabilities.OutputModalities["text"] || len(route.Capabilities.Tags) != 1 {
		t.Fatalf("metadata not propagated: %#v", route.Capabilities)
	}
}

func TestRefreshHookReceivesOnlyNewProviderModelRoutes(t *testing.T) {
	client := &fakeClient{name: "provider-a", models: []providers.Model{{ID: "model-a", Name: "Model A"}}}
	manager := New([]providers.Client{client})
	var batches [][]matcher.Route
	manager.SetRefreshHook(func(_ context.Context, routes []matcher.Route) error {
		batches = append(batches, routes)
		return nil
	})
	if err := manager.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	client.models = append(client.models, providers.Model{ID: "model-b", Name: "Model B"})
	if err := manager.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(batches) != 2 || len(batches[0]) != 1 || batches[0][0].LogicalModel != "model-a" || len(batches[1]) != 1 || batches[1][0].LogicalModel != "model-b" {
		t.Fatalf("unexpected discovery hook batches: %#v", batches)
	}
}
