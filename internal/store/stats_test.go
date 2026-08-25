package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestRequestStatsIncludeUsage(t *testing.T) {
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "payless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.CreateProxyRequest(context.Background(), "request-1", "", "chat_completions", "model-a"); err != nil {
		t.Fatal(err)
	}
	discount := int64(2)
	bps := int64(2857)
	if err := s.RecordUsage(context.Background(), RequestUsage{RequestID: "request-1", InputTokens: 3, OutputTokens: 2, TotalTokens: 5, EstimatedCostPico: 7, OfficialCostPico: 10, DiscountPico: &discount, DiscountBPS: &bps}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordProxyAttempt(context.Background(), "request-1", 2, "surplus", "model-a", "succeeded", "", "", `{"error":{"message":"raw"}}`); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteProxyRequest(context.Background(), "request-1", "succeeded", "", ""); err != nil {
		t.Fatal(err)
	}
	items, err := s.ListRequestStats(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].State != "succeeded" || items[0].DurationMS == nil || items[0].TotalTokens != 5 || items[0].EstimatedCostPico != 7 || items[0].OfficialCostPico == nil || *items[0].OfficialCostPico != 10 || items[0].DiscountPico == nil || *items[0].DiscountPico != 2 || items[0].DiscountBPS == nil || *items[0].DiscountBPS != 2857 || items[0].Provider != "surplus" || items[0].Attempts != 2 || len(items[0].AttemptDetails) != 1 || items[0].AttemptDetails[0].Provider != "surplus" || items[0].AttemptDetails[0].RawError == "" {
		t.Fatalf("unexpected request stats: %#v", items)
	}
	summary, err := s.RequestStatsSummary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if summary.TotalRequests != 1 || summary.SucceededRequests != 1 || summary.TotalAttempts != 2 || summary.RetriedRequests != 1 || summary.RequestsWithTime != 1 || summary.FastestMS == nil || summary.TotalTokens != 5 || summary.EstimatedCostPico != 7 || summary.SavedCostPico != 2 {
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
		if err := s.CreateProxyRequest(context.Background(), item.id, "", "chat_completions", item.model); err != nil {
			t.Fatal(err)
		}
		for _, attempt := range item.attempts {
			if err := s.RecordProxyAttempt(context.Background(), item.id, attempt, "openrouter", item.model, item.state, "", ""); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.CompleteProxyRequest(context.Background(), item.id, item.state, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	items, err := s.ModelStats(context.Background(), map[string]bool{"model-free": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Model != "model-free" || !items[0].Free || items[0].TotalAttempts != 2 || items[0].RetriedRequests != 1 || items[0].SuccessRateBPS != 10000 || items[1].Model != "model-paid" || items[1].Free || items[1].SuccessRateBPS != 0 {
		t.Fatalf("unexpected model stats: %#v", items)
	}
}
