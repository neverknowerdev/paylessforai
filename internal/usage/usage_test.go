package usage

import "testing"

func TestFromJSONNormalizesOpenAIAndAnthropicFields(t *testing.T) {
	stats := FromJSON([]byte(`{"usage":{"prompt_tokens":10,"completion_tokens":4,"prompt_tokens_details":{"cached_tokens":3},"completion_tokens_details":{"reasoning_tokens":2},"cost":0.001}}`))
	if stats.InputTokens != 10 || stats.OutputTokens != 4 || stats.TotalTokens != 14 || stats.CachedReadTokens != 3 || stats.ReasoningTokens != 2 || stats.ActualCostPicoUSD == nil {
		t.Fatalf("unexpected stats: %#v", stats)
	}
	stats = FromJSON([]byte(`{"usage":{"input_tokens":8,"output_tokens":2,"cache_read_input_tokens":5,"cache_creation_input_tokens":1}}`))
	if stats.InputTokens != 8 || stats.OutputTokens != 2 || stats.TotalTokens != 16 || stats.CachedReadTokens != 5 || stats.CacheWriteTokens != 1 || !stats.InputTokensNetOfCache {
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

func TestFromJSONReadsNestedReasoningUsageAcrossFormats(t *testing.T) {
	chat := FromJSON([]byte(`{"usage":{"prompt_tokens":10,"completion_tokens":4,"completion_tokens_details":{"reasoning_tokens":3}}}`))
	if chat.ReasoningTokens != 3 {
		t.Fatalf("Chat reasoning usage = %d", chat.ReasoningTokens)
	}
	responses := FromJSON([]byte(`{"usage":{"input_tokens":10,"output_tokens":4,"output_tokens_details":{"reasoning_tokens":5}}}`))
	if responses.ReasoningTokens != 5 {
		t.Fatalf("Responses reasoning usage = %d", responses.ReasoningTokens)
	}
	anthropic := FromJSON([]byte(`{"usage":{"input_tokens":10,"output_tokens":4,"output_tokens_details":{"thinking_tokens":6}}}`))
	if anthropic.ReasoningTokens != 6 {
		t.Fatalf("Anthropic reasoning usage = %d", anthropic.ReasoningTokens)
	}
}

func TestFromJSONReadsNestedStreamingUsageAndResponsesDetails(t *testing.T) {
	stats := FromJSON([]byte(`{"type":"response.completed","response":{"usage":{"input_tokens":100,"output_tokens":20,"total_tokens":120,"input_tokens_details":{"cached_tokens":60},"output_tokens_details":{"reasoning_tokens":5}}}}`))
	if stats.InputTokens != 100 || stats.OutputTokens != 20 || stats.TotalTokens != 120 || stats.CachedReadTokens != 60 || stats.ReasoningTokens != 5 {
		t.Fatalf("unexpected nested Responses stats: %#v", stats)
	}
	stats = FromJSON([]byte(`{"type":"message_start","message":{"usage":{"input_tokens":40,"output_tokens":1,"cache_read_input_tokens":60,"cache_creation_input_tokens":10}}}`))
	if stats.InputTokens != 40 || stats.OutputTokens != 1 || stats.CachedReadTokens != 60 || stats.CacheWriteTokens != 10 || !stats.InputTokensNetOfCache {
		t.Fatalf("unexpected nested Anthropic stats: %#v", stats)
	}
}
