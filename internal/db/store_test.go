package db

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOpenMigratesFreshDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "payless.db")
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var count int
	if err := s.DB().QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 15 {
		t.Fatalf("got %d migrations", count)
	}
	for _, table := range []string{"settings", "provider_credentials", "client_api_keys", "catalog_refreshes", "models", "model_routes", "provider_health", "proxy_requests", "proxy_attempts", "request_usage", "routing_groups", "routing_group_stages", "routing_group_sources", "routing_group_stage_providers", "routing_group_stage_billing_classes"} {
		var found string
		if err := s.DB().QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&found); err != nil {
			t.Fatalf("table %s: %v", table, err)
		}
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "payless.db")
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := MigrateDatabase(context.Background(), s.DB(), SQLiteDialect); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.DB().QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 15 {
		t.Fatalf("got %d migrations", count)
	}
}
