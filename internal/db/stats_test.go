package db

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/neverknowerdev/paylessforai/internal/db/repositories"
	"time"
)

func TestRequestStatsIncludeUsage(t *testing.T) {
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "payless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ProxyRequests.Create(context.Background(), "request-1", "", "chat_completions", "model-a"); err != nil {
		t.Fatal(err)
	}
	discount := int64(2)
	bps := int64(2857)
	if err := s.RequestUsage.Upsert(context.Background(), RequestUsage{RequestID: "request-1", InputTokens: 3, OutputTokens: 2, TotalTokens: 5, EstimatedCostPico: 7, OfficialCostPico: 10, DiscountPico: &discount, DiscountBPS: &bps}); err != nil {
		t.Fatal(err)
	}
	if err := recordAttempt(s, context.Background(), "request-1", 2, "surplus", "model-a", "succeeded", "", "", `{"error":{"message":"raw"}}`); err != nil {
		t.Fatal(err)
	}
	if err := s.ProxyRequests.Complete(context.Background(), "request-1", "succeeded", "", ""); err != nil {
		t.Fatal(err)
	}
	items, err := s.Stats.ListRequestStats(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].State != "succeeded" || items[0].DurationMS == nil || items[0].TotalTokens != 5 || items[0].EstimatedCostPico != 7 || items[0].OfficialCostPico == nil || *items[0].OfficialCostPico != 10 || items[0].DiscountPico == nil || *items[0].DiscountPico != 2 || items[0].DiscountBPS == nil || *items[0].DiscountBPS != 2857 || items[0].Provider != "surplus" || items[0].Attempts != 2 || len(items[0].AttemptDetails) != 1 || items[0].AttemptDetails[0].Provider != "surplus" || items[0].AttemptDetails[0].DurationMS == nil || items[0].AttemptDetails[0].RawError == "" {
		t.Fatalf("unexpected request stats: %#v", items)
	}
	summary, err := s.Stats.RequestStatsSummary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if summary.TotalRequests != 1 || summary.SucceededRequests != 1 || summary.TotalAttempts != 2 || summary.RetriedRequests != 1 || summary.RequestsWithTime != 1 || summary.FastestMS == nil || summary.TotalTokens != 5 || summary.EstimatedCostPico != 7 || summary.OfficialCostPico != 10 || summary.SavedCostPico != 2 || summary.SavedPercentBPS == nil || *summary.SavedPercentBPS != 2000 {
		t.Fatalf("unexpected summary: %#v", summary)
	}
}

func TestModelStatsAggregatesRetriesAndFreeLabel(t *testing.T) {
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "payless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, item := range []struct {
		id, model, state string
		attempts         []int
	}{
		{"free-request", "model-free", "succeeded", []int{1, 2}},
		{"paid-request", "model-paid", "failed", []int{1}},
	} {
		if err := s.ProxyRequests.Create(context.Background(), item.id, "", "chat_completions", item.model); err != nil {
			t.Fatal(err)
		}
		for _, attempt := range item.attempts {
			if err := recordAttempt(s, context.Background(), item.id, attempt, "openrouter", item.model, item.state, "", ""); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.ProxyRequests.Complete(context.Background(), item.id, item.state, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	items, err := s.Stats.ModelStats(context.Background(), map[string]bool{"model-free": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Model != "model-free" || !items[0].Free || items[0].TotalAttempts != 2 || items[0].RetriedRequests != 1 || items[0].SuccessRateBPS != 10000 || items[1].Model != "model-paid" || items[1].Free || items[1].SuccessRateBPS != 0 {
		t.Fatalf("unexpected model stats: %#v", items)
	}
}

func TestAttemptStatsIncludeDurationForFailedAttempt(t *testing.T) {
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "payless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ProxyRequests.Create(context.Background(), "failed-request", "", "chat_completions", "model-a"); err != nil {
		t.Fatal(err)
	}
	if err := recordAttempt(s, context.Background(), "failed-request", 1, "openrouter", "model-a", "started", "", ""); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	if err := recordAttempt(s, context.Background(), "failed-request", 1, "openrouter", "model-a", "failed", "provider_error", "Provider failed"); err != nil {
		t.Fatal(err)
	}
	items, err := s.Stats.ListRequestStats(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || len(items[0].AttemptDetails) != 1 || items[0].AttemptDetails[0].State != "failed" || items[0].AttemptDetails[0].DurationMS == nil {
		t.Fatalf("expected failed attempt duration, got %#v", items)
	}
}

func TestProviderStatsAggregatesTerminalProviderAndSavings(t *testing.T) {
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "payless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ProxyRequests.Create(context.Background(), "provider-request", "", "chat_completions", "model-a"); err != nil {
		t.Fatal(err)
	}
	if err := recordAttempt(s, context.Background(), "provider-request", 1, "surplus", "model-a", "succeeded", "", ""); err != nil {
		t.Fatal(err)
	}
	discount := int64(25)
	if err := s.RequestUsage.Upsert(context.Background(), RequestUsage{RequestID: "provider-request", InputTokens: 4, OutputTokens: 3, TotalTokens: 7, EstimatedCostPico: 40, OfficialCostPico: 100, ActualCostPico: ptrInt64(75), DiscountPico: &discount}); err != nil {
		t.Fatal(err)
	}
	if err := s.ProxyRequests.Complete(context.Background(), "provider-request", "succeeded", "", ""); err != nil {
		t.Fatal(err)
	}
	items, err := s.Stats.ProviderStats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Provider != "surplus" || items[0].Requests != 1 || items[0].SucceededRequests != 1 || items[0].TotalTokens != 7 || items[0].SavedCostPico != 25 || items[0].DiscountBPS == nil || *items[0].DiscountBPS != 2500 {
		t.Fatalf("unexpected provider stats: %#v", items)
	}
}

func ptrInt64(value int64) *int64 { return &value }

func recordAttempt(repos *repositories.Repositories, ctx context.Context, requestID string, attempt int, provider, upstream, state, errorClass, errorMessage string, rawError ...string) error {
	if err := repos.ProxyRequests.RecordAttemptRoute(ctx, requestID, attempt, provider, upstream); err != nil {
		return err
	}
	return repos.ProxyAttempts.Record(ctx, requestID, attempt, provider, upstream, state, errorClass, errorMessage, rawError...)
}
