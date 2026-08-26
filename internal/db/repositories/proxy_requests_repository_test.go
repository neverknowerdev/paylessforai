package repositories_test

import (
	"testing"
)

func TestProxyRequestsRepositoryIntegration(t *testing.T) {
	i := newIntegrationDB(t)
	i.reset(t)
	if err := i.repos.ProxyRequests.Create(i.ctx, "request-1", "", "chat.completions", "model"); err != nil {
		t.Fatal(err)
	}
	if err := i.repos.ProxyRequests.RecordAttemptRoute(i.ctx, "request-1", 1, "provider", "model"); err != nil {
		t.Fatal(err)
	}
	if err := i.repos.ProxyRequests.Complete(i.ctx, "request-1", "failed", "provider_quota_exhausted", "quota"); err != nil {
		t.Fatal(err)
	}
	var provider, disposition, state string
	var attempts int64
	if err := i.db.QueryRowContext(i.ctx, `SELECT selected_provider, stats_disposition, state, attempt_count FROM proxy_requests WHERE id = $1`, "request-1").Scan(&provider, &disposition, &state, &attempts); err != nil {
		t.Fatal(err)
	}
	if provider != "provider" || disposition != "excluded_limit" || state != "failed" || attempts != 1 {
		t.Fatalf("request row: provider=%q disposition=%q state=%q attempts=%d", provider, disposition, state, attempts)
	}
}
