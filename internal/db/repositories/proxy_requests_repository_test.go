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
	planJSON := `{"group_id":"group-1","selected":"model"}`
	if err := i.repos.ProxyRequests.RecordResolution(i.ctx, "request-1", "group-1", 3, planJSON, "model"); err != nil {
		t.Fatal(err)
	}
	if err := i.repos.ProxyRequests.Complete(i.ctx, "request-1", "failed", "provider_quota_exhausted", "quota"); err != nil {
		t.Fatal(err)
	}
	var provider, disposition, state, resolvedGroup, resolvedPlan, selectedModel string
	var attempts int64
	var revision int64
	if err := i.db.QueryRowContext(i.ctx, `SELECT selected_provider, stats_disposition, state, attempt_count, resolved_group_id, resolved_group_revision, resolved_plan_json, selected_logical_model FROM proxy_requests WHERE id = $1`, "request-1").Scan(&provider, &disposition, &state, &attempts, &resolvedGroup, &revision, &resolvedPlan, &selectedModel); err != nil {
		t.Fatal(err)
	}
	if provider != "provider" || disposition != "excluded_limit" || state != "failed" || attempts != 1 {
		t.Fatalf("request row: provider=%q disposition=%q state=%q attempts=%d", provider, disposition, state, attempts)
	}
	if resolvedGroup != "group-1" || revision != 3 || resolvedPlan != planJSON || selectedModel != "model" {
		t.Fatalf("resolution row: group=%q revision=%d plan=%q selected=%q", resolvedGroup, revision, resolvedPlan, selectedModel)
	}
}
