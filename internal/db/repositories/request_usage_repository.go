package repositories

import "context"

import (
	bobmodels "github.com/neverknowerdev/paylessforai/internal/db/bob/models"
	"github.com/stephenafamo/bob/dialect/sqlite/im"
)

type RequestUsageRepository struct{ bobRepository }

func (r *RequestUsageRepository) Upsert(ctx context.Context, u RequestUsage) error {
	discount := nullableInt64(u.DiscountPico)
	discountBPS := nullableInt64(u.DiscountBPS)
	actual := nullableInt64(u.ActualCostPico)
	setter := &bobmodels.RequestUsageSetter{RequestID: &u.RequestID, InputTokens: &u.InputTokens, OutputTokens: &u.OutputTokens, TotalTokens: &u.TotalTokens, CachedReadTokens: &u.CachedReadTokens, CacheWriteTokens: &u.CacheWriteTokens, ReasoningTokens: &u.ReasoningTokens, EstimatedCostPicoUsd: &u.EstimatedCostPico, OfficialCostPicoUsd: &u.OfficialCostPico, ActualCostPicoUsd: &actual, DiscountPicoUsd: &discount, DiscountPercentBPS: &discountBPS, RawUsageJSON: &u.RawUsageJSON}
	_, err := bobmodels.RequestUsages.Insert(setter, im.OnConflict("request_id").DoUpdate(im.SetExcluded("input_tokens", "output_tokens", "total_tokens", "cached_read_tokens", "cache_write_tokens", "reasoning_tokens", "estimated_cost_pico_usd", "official_cost_pico_usd", "actual_cost_pico_usd", "discount_pico_usd", "discount_percent_bps", "raw_usage_json"))).One(ctx, r.exec)
	return err
}
