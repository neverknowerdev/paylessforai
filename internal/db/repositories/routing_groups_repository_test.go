package repositories_test

import (
	"testing"

	"github.com/neverknowerdev/paylessforai/internal/groups"
	"github.com/neverknowerdev/paylessforai/internal/matcher"
)

func TestRoutingGroupsRepositoryIntegration(t *testing.T) {
	i := newIntegrationDB(t)

	created, err := i.repos.Groups.Save(i.ctx, groups.Definition{
		Name: "Coding", Slug: "Coding", Enabled: true,
		Stages: []groups.Stage{{Sources: []groups.Source{{Kind: groups.SourceModel, ModelID: "model-a", ProviderNames: []string{"OpenRouter"}}}}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := i.repos.Groups.Get(i.ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Slug != "coding" || len(loaded.Stages) != 1 || len(loaded.Stages[0].Sources) != 1 || loaded.Stages[0].Sources[0].ModelID != "model-a" {
		t.Fatalf("unexpected group aggregate: %#v", loaded)
	}
	if len(loaded.Stages[0].Sources[0].ProviderNames) != 1 {
		t.Fatalf("unexpected source providers: %#v", loaded.Stages[0].Sources[0])
	}
	if err := i.repos.Groups.Delete(i.ctx, created.ID, created.Revision); err != nil {
		t.Fatal(err)
	}
}

func TestRoutingGroupsRepositoryIncludesNewProviderRouteAndKeepsFutureProviderRule(t *testing.T) {
	i := newIntegrationDB(t)
	created, err := i.repos.Groups.Save(i.ctx, groups.Definition{
		ID: "future-providers", Name: "Future providers", Slug: "future-providers", Enabled: true,
		Stages: []groups.Stage{{Sources: []groups.Source{{Kind: groups.SourceModel, ModelID: "model-a", IncludeNewProviders: true}}}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	route := matcher.Route{Provider: "new-provider", LogicalModel: "model-a"}
	if err := i.repos.Groups.IncludeDiscoveredRoutes(i.ctx, []matcher.Route{route}); err != nil {
		t.Fatal(err)
	}
	loaded, err := i.repos.Groups.Get(i.ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	sources := loaded.Stages[0].Sources
	if len(sources) != 2 {
		t.Fatalf("expected concrete and future-provider sources, got %#v", sources)
	}
	var concrete, future *groups.Source
	for index := range sources {
		if sources[index].ProviderName == "new-provider" {
			concrete = &sources[index]
		}
		if sources[index].ProviderName == "" && sources[index].IncludeNewProviders {
			future = &sources[index]
		}
	}
	if concrete == nil || concrete.ModelID != "model-a" || concrete.IncludeNewProviders {
		t.Fatalf("expected concrete provider-model source, got %#v", concrete)
	}
	if future == nil || !future.IncludeNewProviders {
		t.Fatalf("future-provider rule was not preserved: %#v", future)
	}
	if err := i.repos.Groups.IncludeDiscoveredRoutes(i.ctx, []matcher.Route{route}); err != nil {
		t.Fatal(err)
	}
	loaded, err = i.repos.Groups.Get(i.ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Stages[0].Sources) != 2 {
		t.Fatalf("discovery sync duplicated provider source: %#v", loaded.Stages[0].Sources)
	}
}
