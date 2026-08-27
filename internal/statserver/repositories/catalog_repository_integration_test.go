package repositories_test

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/neverknowerdev/paylessforai/internal/statserver/models"
)

func TestCatalogRepositorySQL(t *testing.T) {
	f := newIntegrationFixture(t)
	id, err := f.repos.Catalog.UpsertRecord(f.ctx, "fixture", catalogRecord())
	if err != nil {
		t.Fatal(err)
	}
	if count, err := f.repos.Catalog.Count(f.ctx); err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	list, total, err := f.repos.Catalog.List(f.ctx, 10)
	if err != nil || total != 1 || len(list) != 1 {
		t.Fatalf("list=%+v total=%d err=%v", list, total, err)
	}
	search, err := f.repos.Catalog.Search(f.ctx, models.Normalize("deepseek-v4-pro"))
	if err != nil || len(search) != 1 {
		t.Fatalf("search=%+v err=%v", search, err)
	}
	resolved, err := f.repos.Catalog.Resolve(f.ctx, models.Normalize("DeepSeek V4 Pro"))
	if err != nil || resolved.ID != id {
		t.Fatalf("resolve=%+v err=%v", resolved, err)
	}
	detail, err := f.repos.Catalog.Detail(f.ctx, list[0].CanonicalSlug)
	if err != nil || len(detail.Offerings) != 1 || len(detail.Benchmarks) != 1 {
		t.Fatalf("detail=%+v err=%v", detail, err)
	}
	if got, err := f.repos.Catalog.ModelID(f.ctx, list[0].CanonicalSlug); err != nil || got != id {
		t.Fatalf("model id=%d err=%v", got, err)
	}
	ids, err := f.repos.Catalog.ModelIDs(f.ctx)
	if err != nil || len(ids) != 1 {
		t.Fatalf("ids=%v err=%v", ids, err)
	}
	if value, err := f.repos.Catalog.LatestBenchmark(f.ctx, id, "MMLU-Pro"); err != nil || value != .82 {
		t.Fatalf("benchmark=%v err=%v", value, err)
	}
	pricing, total, err := f.repos.Catalog.ListPricing(f.ctx, "deepseek", 10, 0)
	if err != nil || total != 1 || len(pricing) != 1 || pricing[0].OfficialPriceSource == "" || pricing[0].InputUSDPerMillion == nil {
		t.Fatalf("pricing=%+v total=%d err=%v", pricing, total, err)
	}
	if err := f.repos.Users.BootstrapAdmin(f.ctx, "pricing@example.test", "sha256$pricing"); err != nil {
		t.Fatal(err)
	}
	userID, _, err := f.repos.Users.AuthenticateAdmin(f.ctx, "pricing@example.test")
	if err != nil {
		t.Fatal(err)
	}
	input, output := 9.5, 11.5
	if err := f.repos.Catalog.UpdatePriceOverride(f.ctx, pricing[0].OfferingID, userID, models.PriceOverride{InputUSDPerMillion: &input, OutputUSDPerMillion: &output}); err != nil {
		t.Fatal(err)
	}
	pricing, _, err = f.repos.Catalog.ListPricing(f.ctx, "deepseek", 10, 0)
	if err != nil || pricing[0].OverrideInputUSDPerMillion == nil || *pricing[0].InputUSDPerMillion != input || *pricing[0].OutputUSDPerMillion != output {
		t.Fatalf("override pricing=%+v err=%v", pricing, err)
	}
	if _, err := f.repos.Catalog.ModelID(f.ctx, "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing model error=%v", err)
	}
}
