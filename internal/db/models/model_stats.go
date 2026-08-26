package models

type ModelStats struct {
	Model                 string `json:"model"`
	Free                  bool   `json:"free"`
	Requests              int64  `json:"requests"`
	EligibleRequests      int64  `json:"eligible_requests"`
	ExcludedLimitRequests int64  `json:"excluded_limit_requests"`
	SucceededRequests     int64  `json:"succeeded_requests"`
	FailedRequests        int64  `json:"failed_requests"`
	PartialRequests       int64  `json:"partial_requests"`
	SuccessRateBPS        int64  `json:"success_rate_bps"`
	TotalAttempts         int64  `json:"total_attempts"`
	RetriedRequests       int64  `json:"retried_requests"`
	RetryRateBPS          int64  `json:"retry_rate_bps"`
	FastestMS             *int64 `json:"fastest_response_ms,omitempty"`
	SlowestMS             *int64 `json:"slowest_response_ms,omitempty"`
	AverageMS             *int64 `json:"average_response_ms,omitempty"`
	RequestsWithTime      int64  `json:"requests_with_response_time"`
	InputTokens           int64  `json:"input_tokens"`
	OutputTokens          int64  `json:"output_tokens"`
	TotalTokens           int64  `json:"total_tokens"`
	CachedReadTokens      int64  `json:"cached_read_tokens"`
	CacheWriteTokens      int64  `json:"cache_write_tokens"`
	ReasoningTokens       int64  `json:"reasoning_tokens"`
	EstimatedCostPico     int64  `json:"estimated_cost_pico_usd"`
	OfficialCostPico      int64  `json:"official_cost_pico_usd"`
	ActualCostPico        int64  `json:"actual_cost_pico_usd"`
	SavedCostPico         int64  `json:"saved_cost_pico_usd"`
	DiscountPico          int64  `json:"discount_pico_usd"`
	DiscountBPS           *int64 `json:"discount_percent_bps,omitempty"`
}
