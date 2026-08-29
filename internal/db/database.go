package db

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"database/sql"

	"github.com/neverknowerdev/paylessforai/internal/db/repositories"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

func Open(ctx context.Context, path string) (*repositories.Repositories, error) {
	if path == "" {
		return nil, errors.New("database path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	database, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	orm := NewORM(database)
	if err := configure(ctx, database); err != nil {
		database.Close()
		return nil, err
	}
	if err := MigrateDatabase(ctx, database, SQLiteDialect); err != nil {
		database.Close()
		return nil, err
	}
	return repositories.New(orm), nil
}

func configure(ctx context.Context, database *sql.DB) error {
	// PRAGMA statements are SQLite connection setup, not application data
	// access; Bob has no model/query-builder abstraction for them.
	for _, statement := range []string{"PRAGMA foreign_keys = ON", "PRAGMA journal_mode = WAL", "PRAGMA busy_timeout = 5000"} {
		if _, err := database.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("configure sqlite: %s: %w", statement, err)
		}
	}
	return nil
}

type migration struct {
	name     string
	checksum string
	contents string
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.Glob(migrationFS, "migrations/*.sql")
	if err != nil {
		return nil, err
	}
	sort.Strings(entries)
	result := make([]migration, 0, len(entries))
	for _, entry := range entries {
		contents, err := fs.ReadFile(migrationFS, entry)
		if err != nil {
			return nil, err
		}
		result = append(result, migration{name: strings.TrimPrefix(entry, "migrations/"), checksum: checksum(contents), contents: string(contents)})
	}
	return result, nil
}

// MigrateDatabase applies the embedded migration files to an existing SQL
// connection. It is used by the production database opener and integration tests
// so both exercise exactly the same schema history.
func MigrateDatabase(ctx context.Context, database *sql.DB, dialect SQLDialect) error {
	// Migration bookkeeping and DDL intentionally stay as SQL: schema_migrations
	// is bootstrap metadata and the application tables do not exist until their
	// embedded migration files have been applied. All runtime table access uses
	// generated Bob models through repositories.
	if _, err := database.ExecContext(ctx, rebind(`CREATE TABLE IF NOT EXISTS schema_migrations (
		name TEXT PRIMARY KEY,
		checksum TEXT NOT NULL,
		applied_at TEXT NOT NULL
	)`, dialect)); err != nil {
		return fmt.Errorf("create migration table: %w", err)
	}
	migrations, err := loadMigrations()
	if err != nil {
		return fmt.Errorf("load migrations: %w", err)
	}
	for _, migration := range migrations {
		var appliedChecksum string
		err := database.QueryRowContext(ctx, rebind(`SELECT checksum FROM schema_migrations WHERE name = ?`, dialect), migration.name).Scan(&appliedChecksum)
		if err == nil {
			if appliedChecksum != migration.checksum {
				return fmt.Errorf("migration checksum mismatch for %s", migration.name)
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read migration %s: %w", migration.name, err)
		}
		tx, err := database.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", migration.name, err)
		}
		contents := migration.contents
		if dialect == PostgresDialect {
			// Adapt SQLite's type affinities to PostgreSQL without changing the
			// migration bytes/checksums stored in schema_migrations.
			contents = strings.ReplaceAll(contents, " BLOB ", " BYTEA ")
			contents = strings.ReplaceAll(contents, " INTEGER ", " BIGINT ")
		}
		if _, err = tx.ExecContext(ctx, contents); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", migration.name, err)
		}
		if _, err = tx.ExecContext(ctx, rebind(`INSERT INTO schema_migrations(name, checksum, applied_at) VALUES(?, ?, ?)`, dialect), migration.name, migration.checksum, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %s: %w", migration.name, err)
		}
		if err = tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", migration.name, err)
		}
	}
	return nil
}

func checksum(contents []byte) string {
	hash := sha256.Sum256(contents)
	return hex.EncodeToString(hash[:])
}
