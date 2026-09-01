package repositories_test

import (
	"testing"
)

func TestProxyAttemptsRepositoryIntegration(t *testing.T) {
	i := newIntegrationDB(t)
	if err := i.repos.ProxyRequests.Create(i.ctx, "request-1", "", "chat.completions", "model"); err != nil {
		t.Fatal(err)
	}
	if err := i.repos.ProxyAttempts.Record(i.ctx, "request-1", 1, "provider", "model", "started", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := i.repos.ProxyAttempts.Record(i.ctx, "request-1", 1, "provider", "model", "failed", "quota_exhausted", "quota"); err != nil {
		t.Fatal(err)
	}
	stats, err := i.repos.Stats.ListRequestStats(i.ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 || len(stats[0].AttemptDetails) != 1 || stats[0].AttemptDetails[0].State != "failed" || stats[0].AttemptDetails[0].ErrorClass != "quota_exhausted" {
		t.Fatalf("attempt stats: %#v", stats)
	}
}
