package repositories_test

import (
	"errors"
	"testing"

	"github.com/neverknowerdev/paylessforai/internal/statserver/models"
)

func TestSourceRepositorySQL(t *testing.T) {
	f := newIntegrationFixture(t)
	id, err := f.repos.Sources.StartRefresh(f.ctx, models.Source{Key: "fixture", DisplayName: "Fixture", BaseURL: "http://fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.repos.Sources.RecordSnapshot(f.ctx, id, "hash", []byte(`{"models":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := f.repos.Sources.RecordFailure(f.ctx, id, errors.New("temporary")); err != nil {
		t.Fatal(err)
	}
	if err := f.repos.Sources.RecordSuccess(f.ctx, id, 3); err != nil {
		t.Fatal(err)
	}
	items, err := f.repos.Sources.List(f.ctx)
	if err != nil || len(items) != 1 || items[0].RecordCount != 3 || items[0].LastError != "" {
		t.Fatalf("items=%+v err=%v", items, err)
	}
}
