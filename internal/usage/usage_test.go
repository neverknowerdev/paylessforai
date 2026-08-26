package usage

import "testing"

func TestFromJSONNormalizesOpenAIAndAnthropicFields(t *testing.T) {
	stats := FromJSON([]byte(`{"usage":{"prompt_tokens":10,"completion_tokens":4,"prompt_tokens_details":{"cached_tokens":3},"completion_tokens_details":{"reasoning_tokens":2},"cost":0.001}}`))
	if stats.InputTokens != 10 || stats.OutputTokens != 4 || stats.TotalTokens != 14 || stats.CachedReadTokens != 3 || stats.ReasoningTokens != 2 || stats.ActualCostPicoUSD == nil {
		t.Fatalf("unexpected stats: %#v", stats)
	}
	stats = FromJSON([]byte(`{"usage":{"input_tokens":8,"output_tokens":2,"cache_read_input_tokens":5,"cache_creation_input_tokens":1}}`))
	if stats.InputTokens != 8 || stats.OutputTokens != 2 || stats.TotalTokens != 10 || stats.CachedReadTokens != 5 || stats.CacheWriteTokens != 1 || !stats.InputTokensNetOfCache {
		t.Fatalf("unexpected anthropic stats: %#v", stats)
	}
}

func TestFromJSONParsesExactProviderCostShapes(t *testing.T) {
	stats := FromJSON([]byte(`{"usage":{"input_tokens":1,"output_tokens":1,"total_cost":"0.000000123456"}}`))
	if stats.ActualCostPicoUSD == nil || *stats.ActualCostPicoUSD != 123456 {
		t.Fatalf("unexpected exact cost: %#v", stats)
	}
	stats = FromJSON([]byte(`{"response":{"usage":{"input_tokens":2,"output_tokens":3,"cost_usd":0.000001}}}`))
	if stats.ActualCostPicoUSD == nil || *stats.ActualCostPicoUSD != 1_000_000 {
		t.Fatalf("unexpected nested cost: %#v", stats)
	}
	stats = FromJSON([]byte(`{"usage":{"input_tokens":1},"cost_details":{"total":"0.000002"}}`))
	if stats.ActualCostPicoUSD == nil || *stats.ActualCostPicoUSD != 2_000_000 {
		t.Fatalf("unexpected cost details: %#v", stats)
	}
}
