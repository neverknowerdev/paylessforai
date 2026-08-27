package db

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/neverknowerdev/paylessforai/internal/groups"
)

func TestGroupStoreRoundTripsDefinitionAndRevision(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "payless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	value := int64(2)
	retries := 3
	tryRetries := 2
	modelRetries := 4
	auctionPercent := 50
	created, err := store.SaveGroup(context.Background(), groups.Definition{Name: "Coding", Slug: "Coding", Enabled: true, Stages: []groups.Stage{{Name: "Try models", Sources: []groups.Source{{Kind: groups.SourceModel, ModelID: "model-a", ProviderName: "Surplus", Retries: &modelRetries, MaximumOfficialPricePercent: &auctionPercent}}, ProviderNames: []string{"OpenRouter"}, BillingClasses: []groups.BillingClass{groups.BillingMetered}, MaximumInputPicoUSDPerToken: &value, SameRouteRetries: &retries, TryRetries: &tryRetries}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.Slug != "coding" || created.Revision != 1 {
		t.Fatalf("unexpected created group: %#v", created)
	}
	loaded, err := store.GetGroup(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Stages) != 1 || loaded.Stages[0].ProviderNames[0] != "openrouter" || loaded.Stages[0].SameRouteRetries == nil || *loaded.Stages[0].SameRouteRetries != 3 || loaded.Stages[0].TryRetries == nil || *loaded.Stages[0].TryRetries != 2 || loaded.Stages[0].Sources[0].ProviderName != "surplus" || loaded.Stages[0].Sources[0].Retries == nil || *loaded.Stages[0].Sources[0].Retries != 4 || loaded.Stages[0].Sources[0].MaximumOfficialPricePercent == nil || *loaded.Stages[0].Sources[0].MaximumOfficialPricePercent != 50 {
		t.Fatalf("round trip mismatch: %#v", loaded)
	}
	updated, err := store.SaveGroup(context.Background(), groups.Definition{ID: created.ID, Name: "Coding v2", Slug: "coding-v2", Enabled: true, Stages: loaded.Stages}, &created.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 {
		t.Fatalf("expected revision 2, got %d", updated.Revision)
	}
	if _, err := store.SaveGroup(context.Background(), groups.Definition{ID: created.ID, Name: "stale", Slug: "stale", Enabled: true, Stages: loaded.Stages}, &created.Revision); err == nil {
		t.Fatal("expected revision conflict")
	}
}

func TestDeleteReferencedGroupIsProtectedByForeignKey(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "payless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	child, err := store.SaveGroup(context.Background(), groups.Definition{Name: "Child", Slug: "child", Enabled: true, Stages: []groups.Stage{{Position: 0, Sources: []groups.Source{{Kind: groups.SourceModel, ModelID: "model-a"}}, BillingClasses: []groups.BillingClass{groups.BillingMetered}}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := store.SaveGroup(context.Background(), groups.Definition{Name: "Parent", Slug: "parent", Enabled: true, Stages: []groups.Stage{{Position: 0, Sources: []groups.Source{{Kind: groups.SourceGroup, GroupID: child.ID}}, BillingClasses: []groups.BillingClass{groups.BillingMetered}}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteGroup(context.Background(), child.ID, child.Revision); err == nil {
		t.Fatal("expected foreign-key delete failure")
	}
	if _, err := store.GetGroup(context.Background(), parent.ID); err != nil {
		t.Fatal(err)
	}
}
