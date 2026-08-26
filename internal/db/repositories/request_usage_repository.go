package repositories

import "context"

type RequestUsageRepository struct{ db DBTX }

func (r *RequestUsageRepository) Upsert(ctx context.Context, u RequestUsage) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO request_usage(request_id,input_tokens,output_tokens,total_tokens,cached_read_tokens,cache_write_tokens,reasoning_tokens,estimated_cost_pico_usd,official_cost_pico_usd,actual_cost_pico_usd,discount_pico_usd,discount_percent_bps,raw_usage_json) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(request_id) DO UPDATE SET input_tokens=excluded.input_tokens,output_tokens=excluded.output_tokens,total_tokens=excluded.total_tokens,cached_read_tokens=excluded.cached_read_tokens,cache_write_tokens=excluded.cache_write_tokens,reasoning_tokens=excluded.reasoning_tokens,estimated_cost_pico_usd=excluded.estimated_cost_pico_usd,official_cost_pico_usd=excluded.official_cost_pico_usd,actual_cost_pico_usd=excluded.actual_cost_pico_usd,discount_pico_usd=excluded.discount_pico_usd,discount_percent_bps=excluded.discount_percent_bps,raw_usage_json=excluded.raw_usage_json`, u.RequestID, u.InputTokens, u.OutputTokens, u.TotalTokens, u.CachedReadTokens, u.CacheWriteTokens, u.ReasoningTokens, u.EstimatedCostPico, u.OfficialCostPico, u.ActualCostPico, u.DiscountPico, u.DiscountBPS, u.RawUsageJSON)
	return err
}
