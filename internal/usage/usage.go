package usage

import (
	"bytes"
	"encoding/json"
	"math/big"
	"strconv"
)

type Stats struct {
	InputTokens           int64
	OutputTokens          int64
	TotalTokens           int64
	CachedReadTokens      int64
	CacheWriteTokens      int64
	ReasoningTokens       int64
	InputTokensNetOfCache bool
	ActualCostPicoUSD     *int64
	Raw                   map[string]any
}

func FromJSON(body []byte) Stats {
	var envelope map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if decoder.Decode(&envelope) != nil {
		return Stats{}
	}
	return FromEnvelope(envelope)
}

func FromEnvelope(envelope map[string]any) Stats {
	usage, _ := envelope["usage"].(map[string]any)
	if usage == nil {
		if response, ok := envelope["response"].(map[string]any); ok {
			usage, _ = response["usage"].(map[string]any)
		}
	}
	stats := Stats{Raw: usage}
	stats.InputTokens = integer(usage, "prompt_tokens", "input_tokens")
	stats.OutputTokens = integer(usage, "completion_tokens", "output_tokens")
	stats.TotalTokens = integer(usage, "total_tokens")
	_, hasPromptTokens := usage["prompt_tokens"]
	_, hasNetInputTokens := usage["input_tokens"]
	_, hasCacheRead := usage["cache_read_input_tokens"]
	stats.InputTokensNetOfCache = !hasPromptTokens && hasNetInputTokens && hasCacheRead
	if stats.TotalTokens == 0 {
		stats.TotalTokens = stats.InputTokens + stats.OutputTokens
	}
	if details, ok := usage["prompt_tokens_details"].(map[string]any); ok {
		stats.CachedReadTokens = integer(details, "cached_tokens")
		stats.CacheWriteTokens = integer(details, "cache_write_tokens")
	}
	if stats.CachedReadTokens == 0 {
		stats.CachedReadTokens = integer(usage, "cache_read_input_tokens", "cached_tokens")
	}
	if stats.CacheWriteTokens == 0 {
		stats.CacheWriteTokens = integer(usage, "cache_creation_input_tokens", "cache_write_tokens")
	}
	if details, ok := usage["completion_tokens_details"].(map[string]any); ok {
		stats.ReasoningTokens = integer(details, "reasoning_tokens")
	}
	if stats.ReasoningTokens == 0 {
		stats.ReasoningTokens = integer(usage, "reasoning_tokens")
	}
	for _, values := range []map[string]any{usage, envelope} {
		for _, name := range []string{"cost", "total_cost", "cost_usd", "total_cost_usd", "price"} {
			if pico, ok := picoUSD(values[name]); ok {
				stats.ActualCostPicoUSD = &pico
				break
			}
		}
		if stats.ActualCostPicoUSD != nil {
			break
		}
		if details, ok := values["cost_details"].(map[string]any); ok {
			for _, name := range []string{"total", "cost", "total_cost", "usd"} {
				if pico, ok := picoUSD(details[name]); ok {
					stats.ActualCostPicoUSD = &pico
					break
				}
			}
		}
		if stats.ActualCostPicoUSD != nil {
			break
		}
	}
	return stats
}

func integer(values map[string]any, names ...string) int64 {
	for _, name := range names {
		if value, ok := number(values, name); ok {
			return int64(value)
		}
	}
	return 0
}

func number(values map[string]any, name string) (float64, bool) {
	value, ok := values[name]
	if !ok {
		return 0, false
	}
	switch typed := value.(type) {
	case float64:
		return typed, true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseFloat(typed, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func picoUSD(value any) (int64, bool) {
	var text string
	switch typed := value.(type) {
	case string:
		text = typed
	case json.Number:
		text = typed.String()
	case float64:
		text = strconv.FormatFloat(typed, 'f', -1, 64)
	case int64:
		text = strconv.FormatInt(typed, 10)
	default:
		return 0, false
	}
	rational, ok := new(big.Rat).SetString(text)
	if !ok || rational.Sign() < 0 {
		return 0, false
	}
	rational.Mul(rational, big.NewRat(1_000_000_000_000, 1))
	if !rational.IsInt() || !rational.Num().IsInt64() {
		return 0, false
	}
	return rational.Num().Int64(), true
}
