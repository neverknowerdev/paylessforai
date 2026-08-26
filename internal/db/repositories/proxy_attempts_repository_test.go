package repositories_test

import (
	"testing"
)

func TestProxyAttemptsRepositoryIntegration(t *testing.T) {
	i := newIntegrationDB(t)
	i.reset(t)
	if err := i.repos.ProxyRequests.Create(i.ctx, "request-1", "", "chat.completions", "model"); err != nil {
		t.Fatal(err)
	}
	if err := i.repos.ProxyAttempts.Record(i.ctx, "request-1", 1, "provider", "model", "started", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := i.repos.ProxyAttempts.Record(i.ctx, "request-1", 1, "provider", "model", "failed", "quota_exhausted", "quota"); err != nil {
		t.Fatal(err)
	}
	var state, disposition string
	var completedAt *string
	if err := i.db.QueryRowContext(i.ctx, `SELECT state, stats_disposition, completed_at FROM proxy_attempts WHERE id = $1`, "request-1:1").Scan(&state, &disposition, &completedAt); err != nil {
		t.Fatal(err)
	}
	if state != "failed" || disposition != "excluded_limit" || completedAt == nil {
		t.Fatalf("attempt row: state=%q disposition=%q completed_at=%v", state, disposition, completedAt)
	}
}
