package repositories_test

import (
	"testing"

	"github.com/neverknowerdev/paylessforai/internal/groups"
)

func TestRoutingGroupsRepositoryIntegration(t *testing.T) {
	i := newIntegrationDB(t)
	i.reset(t)

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
