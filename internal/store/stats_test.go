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
	if err := s.RecordUsage(context.Background(), RequestUsage{RequestID: "request-1", InputTokens: 3, OutputTokens: 2, TotalTokens: 5, EstimatedCostPico: 7}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordProxyAttempt(context.Background(), "request-1", 2, "surplus", "model-a", "succeeded", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteProxyRequest(context.Background(), "request-1", "succeeded", "", ""); err != nil {
		t.Fatal(err)
	}
	items, err := s.ListRequestStats(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].State != "succeeded" || items[0].TotalTokens != 5 || items[0].EstimatedCostPico != 7 || items[0].Provider != "surplus" || items[0].Attempts != 2 {
		t.Fatalf("unexpected request stats: %#v", items)
	}
	summary, err := s.RequestStatsSummary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if summary.TotalRequests != 1 || summary.SucceededRequests != 1 || summary.TotalTokens != 5 || summary.EstimatedCostPico != 7 {
		t.Fatalf("unexpected summary: %#v", summary)
	}
}
