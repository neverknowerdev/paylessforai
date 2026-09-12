package models

type RequestStat struct {
	ID                string             `json:"id"`
	SessionID         *string            `json:"session_id,omitempty"`
	Protocol          string             `json:"protocol"`
	Model             string             `json:"model"`
	State             string             `json:"state"`
	ReceivedAt        string             `json:"received_at"`
	CompletedAt       *string            `json:"completed_at,omitempty"`
	DurationMS        *int64             `json:"duration_ms,omitempty"`
	ErrorCode         *string            `json:"error_code,omitempty"`
	ErrorMessage      *string            `json:"error_message,omitempty"`
	InputTokens       int64              `json:"input_tokens"`
	OutputTokens      int64              `json:"output_tokens"`
	TotalTokens       int64              `json:"total_tokens"`
	CachedReadTokens  int64              `json:"cached_read_tokens"`
	CacheWriteTokens  int64              `json:"cache_write_tokens"`
	ReasoningTokens   int64              `json:"reasoning_tokens"`
	EstimatedCostPico int64              `json:"estimated_cost_pico_usd"`
	OfficialCostPico  *int64             `json:"official_cost_pico_usd,omitempty"`
	ActualCostPico    *int64             `json:"actual_cost_pico_usd,omitempty"`
	DiscountPico      *int64             `json:"discount_pico_usd,omitempty"`
	DiscountBPS       *int64             `json:"discount_percent_bps,omitempty"`
	Provider          string             `json:"provider,omitempty"`
	UpstreamModel     string             `json:"upstream_model,omitempty"`
	Attempts          int64              `json:"attempts"`
	AttemptDetails    []AttemptStat      `json:"attempt_details,omitempty"`
	SkippedRoutes     []SkippedRouteStat `json:"skipped_routes,omitempty"`
}

// SkippedRouteStat describes a planned route that was rejected before any
// upstream request was sent.
type SkippedRouteStat struct {
	RouteID       string `json:"route_id,omitempty"`
	Provider      string `json:"provider,omitempty"`
	UpstreamModel string `json:"upstream_model,omitempty"`
	State         string `json:"state"`
	ReasonCode    string `json:"reason_code"`
	Reason        string `json:"reason"`
}
