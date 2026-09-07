package models

import "time"

type TelemetryEvent struct {
	EventID                 string    `json:"event_id"`
	ModelName               string    `json:"model_name"`
	Provider                string    `json:"provider"`
	OccurredAt              time.Time `json:"occurred_at"`
	TotalMS                 int       `json:"total_ms"`
	TTFTMS                  int       `json:"ttft_ms"`
	GenerationMS            int       `json:"generation_ms"`
	InputTokens             int       `json:"input_tokens"`
	OutputTokens            int       `json:"output_tokens"`
	CachedReadTokens        int       `json:"cached_read_tokens"`
	CacheWriteTokens        int       `json:"cache_write_tokens"`
	CacheTTLSeconds         int       `json:"cache_ttl_seconds"`
	ObservedReuseAgeSeconds int       `json:"observed_reuse_age_seconds"`
	RetryCount              int       `json:"retry_count"`
	CacheStatus             string    `json:"cache_status"`
	Success                 bool      `json:"success"`
	CostUSD                 *float64  `json:"cost_usd"`
}

type TelemetryBatch struct {
	BatchID string           `json:"batch_id"`
	Events  []TelemetryEvent `json:"events"`
}

type Statistics struct {
	SampleCount         int     `json:"sample_count"`
	SuccessCount        int     `json:"success_count"`
	CacheHits           int     `json:"cache_hits"`
	CacheMisses         int     `json:"cache_misses"`
	CacheWrites         int     `json:"cache_writes"`
	SuccessRate         float64 `json:"success_rate"`
	AverageTotalMS      float64 `json:"avg_total_ms"`
	P50TotalMS          float64 `json:"p50_total_ms"`
	AverageTTFTMS       float64 `json:"avg_ttft_ms"`
	AverageGenerationMS float64 `json:"avg_generation_ms"`
	AverageInputTokens  float64 `json:"avg_input_tokens"`
	AverageOutputTokens float64 `json:"avg_output_tokens"`
	CacheHitRate        float64 `json:"cache_hit_rate"`
	Provider            string  `json:"provider"`
	Model               string  `json:"model"`
}
