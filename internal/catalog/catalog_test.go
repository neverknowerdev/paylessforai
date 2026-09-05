package catalog

import (
	"context"
	"fmt"
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

type memoryState struct {
	value string
	fail  bool
}

func (s *memoryState) Get(context.Context, string) (string, bool, error) {
	return s.value, s.value != "", nil
}
func (s *memoryState) Set(_ context.Context, _, value string) error {
	if s.fail {
		return fmt.Errorf("storage unavailable")
	}
	s.value = value
	return nil
}

func TestDiscoveryHistorySurvivesRestartAndDoesNotRepeat(t *testing.T) {
	ctx := context.Background()
	store := &memoryState{}
	client := &fakeClient{name: "surplus", models: []providers.Model{{ID: "old"}}}
	manager := New([]providers.Client{client})
	if err := manager.Restore(ctx, store); err != nil {
		t.Fatal(err)
	}
	if err := manager.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if len(manager.Snapshot().Additions) != 0 {
		t.Fatal("initial catalog must be a baseline")
	}
	client.models = append(client.models, providers.Model{ID: "new"})
	if err := manager.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	added := manager.Snapshot().Additions
	if len(added) != 1 || added[0].ID != "new" {
		t.Fatalf("additions: %#v", added)
	}
	manager = New([]providers.Client{client})
	if err := manager.Restore(ctx, store); err != nil {
		t.Fatal(err)
	}
	if err := manager.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if got := manager.Snapshot().Additions; len(got) != 1 || !got[0].AddedAt.Equal(added[0].AddedAt) {
		t.Fatalf("restart changed history: %#v", got)
	}
	client.models = client.models[:1]
	if err := manager.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	client.models = append(client.models, providers.Model{ID: "new"})
	if err := manager.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if len(manager.Snapshot().Additions) != 1 {
		t.Fatal("returning model announced twice")
	}
}

func TestDiscoveryPersistenceFailureRetriesWithoutLosingAddition(t *testing.T) {
	ctx := context.Background()
	store := &memoryState{}
	client := &fakeClient{name: "surplus", models: []providers.Model{{ID: "old"}}}
	manager := New([]providers.Client{client})
	if err := manager.Restore(ctx, store); err != nil {
		t.Fatal(err)
	}
	if err := manager.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	client.models = append(client.models, providers.Model{ID: "new"})
	store.fail = true
	if err := manager.Refresh(ctx); err == nil {
		t.Fatal("expected storage failure")
	}
	if len(manager.Snapshot().Models) != 1 {
		t.Fatal("published unpersisted discovery")
	}
	store.fail = false
	if err := manager.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if len(manager.Snapshot().Additions) != 1 {
		t.Fatal("lost addition after retry")
	}
}

type failingClient struct{ fakeClient }

func (f failingClient) Discover(context.Context) ([]providers.Model, error) {
	return nil, fmt.Errorf("offline")
}
func TestPartialRefreshRetainsFailedProviderButRemovesDeletedProvider(t *testing.T) {
	ctx := context.Background()
	a := fakeClient{name: "a", models: []providers.Model{{ID: "a"}}}
	b := fakeClient{name: "b", models: []providers.Model{{ID: "b"}}}
	manager := New([]providers.Client{a, b})
	if err := manager.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	manager.SetClients([]providers.Client{a, failingClient{b}})
	if err := manager.Refresh(ctx); err == nil {
		t.Fatal("expected partial failure")
	}
	if len(manager.Snapshot().Routes) != 2 {
		t.Fatal("failed provider routes lost")
	}
	manager.SetClients([]providers.Client{a})
	if err := manager.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if len(manager.Snapshot().Routes) != 1 {
		t.Fatal("deleted provider retained")
	}
	manager.SetClients(nil)
	if err := manager.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if len(manager.Snapshot().Routes) != 0 {
		t.Fatal("last provider retained")
	}
}

func TestRefreshAliasesAreProviderIndependent(t *testing.T) {
	for _, names := range [][2]string{{"custom-a", "custom-b"}, {"surplus", "openrouter"}, {"openrouter", "surplus"}} {
		for _, reverse := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%s/reverse=%t", names[0], names[1], reverse), func(t *testing.T) {
				qualified := fakeClient{name: names[0], models: []providers.Model{{ID: "vendor/model-a"}}}
				bare := fakeClient{name: names[1], models: []providers.Model{{ID: "model-a:free", Free: true}}}
				clients := []providers.Client{qualified, bare}
				if reverse {
					clients[0], clients[1] = clients[1], clients[0]
				}
				manager := New(clients)
				ctx := context.Background()
				assertMerged := func() {
					t.Helper()
					snapshot := manager.Snapshot()
					if len(snapshot.Models) != 1 || snapshot.Models[0].ID != "vendor/model-a" || len(snapshot.Routes) != 2 {
						t.Fatalf("aliases not merged: %#v", snapshot)
					}
					for _, route := range snapshot.Routes {
						if route.LogicalModel != "vendor/model-a" {
							t.Fatalf("incorrect alias: %#v", route)
						}
					}
					if len(snapshot.Additions) != 0 {
						t.Fatalf("outage announced existing model: %#v", snapshot.Additions)
					}
				}
				if err := manager.Refresh(ctx); err != nil {
					t.Fatal(err)
				}
				assertMerged()
				// Either provider may fail; both failure directions preserve the identity.
				for _, failed := range [][]providers.Client{{failingClient{qualified}, bare}, {qualified, failingClient{bare}}} {
					manager.SetClients(failed)
					if err := manager.Refresh(ctx); err == nil {
						t.Fatal("expected partial refresh failure")
					}
					assertMerged()
				}
				manager.SetClients(clients)
				if err := manager.Refresh(ctx); err != nil {
					t.Fatal(err)
				}
				assertMerged()
			})
		}
	}
}

func TestLogicalModelDoesNotGuessAmbiguousAliases(t *testing.T) {
	for _, ids := range [][]string{{"vendor-a/model", "vendor-b/model", "model"}, {"model", "vendor-b/model", "vendor-a/model"}} {
		for _, id := range ids {
			if got := logicalModel(id, ids); got != id {
				t.Fatalf("ambiguous alias %q became %q", id, got)
			}
		}
	}
	if got := logicalModel("model", []string{"vendor/model", "vendor/model:free", "vendor/model"}); got != "vendor/model" {
		t.Fatalf("duplicate variants must not create ambiguity: %q", got)
	}
}
