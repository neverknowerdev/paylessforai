package repositories

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stephenafamo/bob"
	_ "modernc.org/sqlite"
)

func TestModelsRepositoryUsesBobGeneratedTable(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`CREATE TABLE models (
		id TEXT PRIMARY KEY,
		display_name TEXT NOT NULL,
		context_length INTEGER NOT NULL DEFAULT 0,
		max_output_tokens INTEGER NOT NULL DEFAULT 0,
		metadata_json TEXT NOT NULL DEFAULT '{}',
		observed_at TEXT NOT NULL,
		stale_at TEXT
	)`); err != nil {
		t.Fatal(err)
	}

	exec := bob.NewDB(database)
	repo := &ModelsRepository{db: databaseTX{database}, bob: exec}
	ctx := context.Background()
	model := ModelRecord{ID: "model-1", DisplayName: "Bob model", ContextLength: 8192, ObservedAt: "2026-08-26T00:00:00Z"}
	if err := repo.Upsert(ctx, model); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, model.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.DisplayName != model.DisplayName || got.ContextLength != model.ContextLength {
		t.Fatalf("got %+v, want %+v", got, model)
	}
	if err := repo.DeleteAll(ctx); err != nil {
		t.Fatal(err)
	}
}

// databaseTX adapts *sql.DB to the repository's legacy fallback interface.
// The Bob path above is the code under test.
type databaseTX struct{ *sql.DB }
