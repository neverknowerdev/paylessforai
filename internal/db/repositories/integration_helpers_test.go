package repositories_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/neverknowerdev/paylessforai/internal/db"
	"github.com/neverknowerdev/paylessforai/internal/db/repositories"
	_ "modernc.org/sqlite"
)

type integrationDB struct {
	ctx   context.Context
	repos *repositories.Repositories
}

func newIntegrationDB(t *testing.T) *integrationDB {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "integration.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if _, err := database.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		t.Fatal(err)
	}
	if err := database.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateDatabase(ctx, database, db.SQLiteDialect); err != nil {
		t.Fatalf("apply project migrations: %v", err)
	}
	return &integrationDB{ctx: ctx, repos: repositories.New(db.NewORM(database))}
}
