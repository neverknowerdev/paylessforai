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
