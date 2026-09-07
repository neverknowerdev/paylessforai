package repositories_test

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	statdb "github.com/neverknowerdev/paylessforai/internal/statserver/db"
	"github.com/neverknowerdev/paylessforai/internal/statserver/models"
	"github.com/neverknowerdev/paylessforai/internal/statserver/repositories"
)

type integrationFixture struct {
	ctx   context.Context
	db    *sql.DB
	repos *repositories.Repositories
}

func newIntegrationFixture(t *testing.T) *integrationFixture {
	t.Helper()
	dsn := os.Getenv("STAT_SERVER_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("STAT_SERVER_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	database, err := statdb.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := statdb.Migrate(ctx, database); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `TRUNCATE TABLE sources,models,telemetry_installations,capability_profiles,manual_score_signals,users RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
	return &integrationFixture{ctx: ctx, db: database, repos: repositories.New(database)}
}

func catalogRecord() models.CatalogRecord {
	input, output := 1.25, 2.5
	return models.CatalogRecord{SourceID: "fixture/model", Name: "DeepSeek V4 Pro", Creator: "DeepSeek", Revision: "0423", Context: 128000, ProviderModel: "deepseek-v4-pro", Input: &input, Output: &output, Benchmarks: map[string]float64{"MMLU Pro": .82}, Metadata: map[string]any{"fixture": true}}
}
