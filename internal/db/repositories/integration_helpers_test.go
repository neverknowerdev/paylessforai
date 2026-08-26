package repositories_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/neverknowerdev/paylessforai/internal/db"
	bobmodels "github.com/neverknowerdev/paylessforai/internal/db/bob/models"
	"github.com/neverknowerdev/paylessforai/internal/db/repositories"
	"github.com/stephenafamo/bob"
	"github.com/stephenafamo/bob/dialect/sqlite"
	"github.com/stephenafamo/bob/dialect/sqlite/dm"
	_ "modernc.org/sqlite"
)

type integrationDB struct {
	ctx   context.Context
	db    *sql.DB
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
	return &integrationDB{ctx: ctx, db: database, repos: repositories.New(db.NewORM(database))}
}

func (i *integrationDB) reset(t *testing.T) {
	t.Helper()
	exec := bob.NewDB(i.db)
	for _, table := range []bob.Expression{
		bobmodels.RequestUsages.NameAsExpr(),
		bobmodels.ProxyAttempts.NameAsExpr(),
		bobmodels.ProxyRequests.NameAsExpr(),
		bobmodels.ProviderHealths.NameAsExpr(),
		bobmodels.ModelRoutes.NameAsExpr(),
		bobmodels.Models.NameAsExpr(),
		bobmodels.CatalogRefreshes.NameAsExpr(),
		bobmodels.ClientAPIKeys.NameAsExpr(),
		bobmodels.ProviderCredentials.NameAsExpr(),
		bobmodels.Settings.NameAsExpr(),
	} {
		if _, err := bob.Exec(i.ctx, exec, sqlite.Delete(dm.From(table))); err != nil {
			t.Fatal(err)
		}
	}
}
