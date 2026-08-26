package models

type RequestUsage struct {
	RequestID         string
	InputTokens       int64
	OutputTokens      int64
	TotalTokens       int64
	CachedReadTokens  int64
	CacheWriteTokens  int64
	ReasoningTokens   int64
	EstimatedCostPico int64
	OfficialCostPico  int64
	ActualCostPico    *int64
	DiscountPico      *int64
	DiscountBPS       *int64
	RawUsageJSON      string
}
