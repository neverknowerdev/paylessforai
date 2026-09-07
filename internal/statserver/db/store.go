// Package db owns PostgreSQL connectivity and forward-only schema migration.
package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"

	_ "github.com/lib/pq"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

func Open(ctx context.Context, url string) (*sql.DB, error) {
	database, err := sql.Open("postgres", url)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	database.SetMaxOpenConns(12)
	database.SetMaxIdleConns(4)
	if err := database.PingContext(ctx); err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return database, nil
}

func Migrate(ctx context.Context, database *sql.DB) error {
	journal, err := migrationFS.ReadFile("migrations/0000_schema_migrations.sql")
	if err != nil {
		return fmt.Errorf("read migration journal: %w", err)
	}
	if _, err := database.ExecContext(ctx, string(journal)); err != nil {
		return fmt.Errorf("create migration journal: %w", err)
	}
	files, err := fs.Glob(migrationFS, "migrations/*.sql")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	sort.Strings(files)
	for _, file := range files {
		if file == "migrations/0000_schema_migrations.sql" {
			continue
		}
		var exists bool
		if err := database.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM stat_server_schema_migrations WHERE version=$1)`, file).Scan(&exists); err != nil {
			return fmt.Errorf("check migration %s: %w", file, err)
		}
		if exists {
			continue
		}
		sqlText, err := migrationFS.ReadFile(file)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", file, err)
		}
		tx, err := database.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", file, err)
		}
		if _, err = tx.ExecContext(ctx, string(sqlText)); err == nil {
			_, err = tx.ExecContext(ctx, `INSERT INTO stat_server_schema_migrations(version) VALUES($1)`, file)
		}
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", file, err)
		}
		if err = tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", file, err)
		}
	}
	return nil
}
