package repositories_test

import (
	"testing"
)

func TestProxyRequestsRepositoryIntegration(t *testing.T) {
	i := newIntegrationDB(t)
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
	stats, err := i.repos.Stats.ListRequestStats(i.ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 || stats[0].Provider != "provider" || stats[0].State != "failed" || stats[0].Attempts != 1 {
		t.Fatalf("request stats: %#v", stats)
	}
	if stats[0].UpstreamModel != "model" {
		t.Fatalf("resolution stats: %#v", stats[0])
	}
}

func TestProxyRequestsRecordResolutionWithoutSelectedRoute(t *testing.T) {
	i := newIntegrationDB(t)
	if err := i.repos.ProxyRequests.Create(i.ctx, "request-no-route", "", "chat.completions", "model-a"); err != nil {
		t.Fatal(err)
	}
	plan := `{"requested_model":"model-a","rejections":[{"code":"missing_capability"}]}`
	if err := i.repos.ProxyRequests.RecordResolution(i.ctx, "request-no-route", "", 0, plan, ""); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := i.repos.DB().QueryRowContext(i.ctx, `SELECT resolved_plan_json FROM proxy_requests WHERE id = ?`, "request-no-route").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != plan {
		t.Fatalf("expected unresolved route plan to be persisted, got %q", stored)
	}
}
