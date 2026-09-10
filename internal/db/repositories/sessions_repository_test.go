package repositories_test

import (
	"testing"
	"time"
)

func TestSessionRepositoryRetainsRecentHistoryAndScopesClients(t *testing.T) {
	i := newIntegrationDB(t)
	now := time.Unix(1000, 0).UTC()
	first, err := i.repos.Sessions.ResolveAndRegister(i.ctx, "client-a", []string{"long-1", "anchor-1"}, "anchor-1", "long-1", "session-1", now)
	if err != nil || first != "session-1" {
		t.Fatalf("first detection: %q, %v", first, err)
	}
	second, err := i.repos.Sessions.ResolveAndRegister(i.ctx, "client-a", []string{"long-2", "long-1", "anchor-1"}, "anchor-1", "long-2", "session-2", now.Add(time.Second))
	if err != nil || second != "session-1" {
		t.Fatalf("prefix continuation: %q, %v", second, err)
	}
	other, err := i.repos.Sessions.ResolveAndRegister(i.ctx, "client-b", []string{"long-1"}, "long-1", "long-1", "session-b", now)
	if err != nil || other != "session-b" {
		t.Fatalf("client namespace: %q, %v", other, err)
	}
	var sessions, keys int
	if err := i.repos.DB().QueryRowContext(i.ctx, `SELECT count(*) FROM sessions`).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if err := i.repos.DB().QueryRowContext(i.ctx, `SELECT count(*) FROM session_keys`).Scan(&keys); err != nil {
		t.Fatal(err)
	}
	if sessions != 2 || keys != 4 {
		t.Fatalf("unexpected persisted sessions/keys: %d/%d", sessions, keys)
	}
}

func TestSessionRepositoryAmbiguityDoesNotFallBackToShorterPrefix(t *testing.T) {
	i := newIntegrationDB(t)
	now := time.Unix(2000, 0).UTC()
	for _, sessionID := range []string{"session-1", "session-2"} {
		stamp := now.Format(time.RFC3339Nano)
		if _, err := i.repos.DB().ExecContext(i.ctx, `INSERT INTO sessions(client_key_id, session_id, created_at, last_seen_at) VALUES(?, ?, ?, ?)`, "client", sessionID, stamp, stamp); err != nil {
			t.Fatal(err)
		}
		if _, err := i.repos.DB().ExecContext(i.ctx, `INSERT INTO session_keys(client_key_id, session_id, key_hash, is_anchor, last_seen_at) VALUES(?, ?, ?, ?, ?)`, "client", sessionID, "shared", 1, stamp); err != nil {
			t.Fatal(err)
		}
	}
	got, err := i.repos.Sessions.ResolveAndRegister(i.ctx, "client", []string{"unknown-long", "shared"}, "shared", "unknown-long", "fresh", now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if got != "fresh" {
		t.Fatalf("ambiguous shorter prefix must not be reused: %q", got)
	}
}
