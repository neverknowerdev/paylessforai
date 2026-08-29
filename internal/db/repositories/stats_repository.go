package repositories

import (
	"context"
	"database/sql"

	"github.com/neverknowerdev/paylessforai/internal/db/models"
)

type StatsRepository struct{ database *sql.DB }

type RequestStat = models.RequestStat
type AttemptStat = models.AttemptStat
type StatsSummary = models.StatsSummary
type ModelStats = models.ModelStats
type ProviderStats = models.ProviderStats

func (r *StatsRepository) ListRequestStats(ctx context.Context, limit int) ([]RequestStat, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.database.QueryContext(ctx, `SELECT r.id, r.protocol, r.logical_model, r.state, r.received_at, r.completed_at, r.duration_ms, r.error_code,
		COALESCE(r.selected_provider, ''), COALESCE(r.selected_upstream_model, ''), r.attempt_count,
		u.input_tokens, u.output_tokens, u.total_tokens, u.cached_read_tokens, u.cache_write_tokens, u.reasoning_tokens,
		u.estimated_cost_pico_usd, u.official_cost_pico_usd, u.actual_cost_pico_usd, u.discount_pico_usd, u.discount_percent_bps
		FROM proxy_requests r LEFT JOIN request_usage u ON u.request_id = r.id
		ORDER BY r.received_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	result := make([]RequestStat, 0)
	for rows.Next() {
		var item RequestStat
		var completedAt, errorCode sql.NullString
		var duration sql.NullInt64
		var input, output, total, cachedRead, cacheWrite, reasoning, estimated sql.NullInt64
		var official, actual, discount, discountBPS sql.NullInt64
		if err := rows.Scan(&item.ID, &item.Protocol, &item.Model, &item.State, &item.ReceivedAt, &completedAt, &duration, &errorCode,
			&item.Provider, &item.UpstreamModel, &item.Attempts, &input, &output, &total, &cachedRead, &cacheWrite, &reasoning, &estimated, &official, &actual, &discount, &discountBPS); err != nil {
			return nil, err
		}
		if completedAt.Valid {
			item.CompletedAt = &completedAt.String
		}
		if errorCode.Valid {
			item.ErrorCode = &errorCode.String
		}
		if duration.Valid {
			item.DurationMS = &duration.Int64
		}
		item.InputTokens, item.OutputTokens, item.TotalTokens = input.Int64, output.Int64, total.Int64
		item.CachedReadTokens, item.CacheWriteTokens, item.ReasoningTokens = cachedRead.Int64, cacheWrite.Int64, reasoning.Int64
		item.EstimatedCostPico = estimated.Int64
		if official.Valid {
			item.OfficialCostPico = &official.Int64
		}
		if actual.Valid {
			item.ActualCostPico = &actual.Int64
		}
		if discount.Valid {
			item.DiscountPico = &discount.Int64
		}
		if discountBPS.Valid {
			item.DiscountBPS = &discountBPS.Int64
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for index := range result {
		attemptRows, err := r.database.QueryContext(ctx, `SELECT attempt_number, COALESCE(provider, ''), COALESCE(upstream_model, ''), state, started_at, COALESCE(completed_at, ''), duration_ms, COALESCE(error_class, ''), COALESCE(error_message, ''), COALESCE(error_raw, '') FROM proxy_attempts WHERE request_id = ? ORDER BY attempt_number`, result[index].ID)
		if err != nil {
			return nil, err
		}
		for attemptRows.Next() {
			var attempt AttemptStat
			var duration sql.NullInt64
			if err := attemptRows.Scan(&attempt.Number, &attempt.Provider, &attempt.UpstreamModel, &attempt.State, &attempt.StartedAt, &attempt.CompletedAt, &duration, &attempt.ErrorClass, &attempt.ErrorMessage, &attempt.RawError); err != nil {
				attemptRows.Close()
				return nil, err
			}
			if duration.Valid {
				attempt.DurationMS = &duration.Int64
			}
			result[index].AttemptDetails = append(result[index].AttemptDetails, attempt)
		}
		if err := attemptRows.Err(); err != nil {
			attemptRows.Close()
			return nil, err
		}
		if err := attemptRows.Close(); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (r *StatsRepository) RequestStatsSummary(ctx context.Context) (StatsSummary, error) {
	var summary StatsSummary
	err := r.database.QueryRowContext(ctx, `SELECT
		COUNT(*),
		COALESCE(SUM(CASE WHEN r.state = 'succeeded' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN r.state = 'failed' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN r.state = 'partial' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(u.input_tokens), 0),
		COALESCE(SUM(u.output_tokens), 0),
		COALESCE(SUM(u.total_tokens), 0),
		COALESCE(SUM(u.cached_read_tokens), 0),
		COALESCE(SUM(u.cache_write_tokens), 0),
		COALESCE(SUM(u.reasoning_tokens), 0),
		COALESCE(SUM(u.estimated_cost_pico_usd), 0),
		COALESCE(SUM(u.official_cost_pico_usd), 0),
		COALESCE(SUM(u.actual_cost_pico_usd), 0),
		COALESCE(SUM(CASE WHEN u.discount_pico_usd > 0 THEN u.discount_pico_usd ELSE 0 END), 0),
		COUNT(u.actual_cost_pico_usd),
		COALESCE(SUM(r.attempt_count), 0),
		COALESCE(SUM(CASE WHEN r.attempt_count > 1 THEN 1 ELSE 0 END), 0),
		MIN(r.duration_ms), MAX(r.duration_ms), CAST(AVG(r.duration_ms) AS INTEGER), COUNT(r.duration_ms)
		FROM proxy_requests r LEFT JOIN request_usage u ON u.request_id = r.id`).Scan(
		&summary.TotalRequests, &summary.SucceededRequests, &summary.FailedRequests, &summary.PartialRequests,
		&summary.InputTokens, &summary.OutputTokens, &summary.TotalTokens, &summary.CachedReadTokens,
		&summary.CacheWriteTokens, &summary.ReasoningTokens, &summary.EstimatedCostPico,
		&summary.OfficialCostPico, &summary.ActualCostPico, &summary.SavedCostPico, &summary.RequestsWithActual,
		&summary.TotalAttempts, &summary.RetriedRequests, &summary.FastestMS, &summary.SlowestMS, &summary.AverageMS, &summary.RequestsWithTime)
	if err == nil && summary.OfficialCostPico > 0 {
		value := summary.SavedCostPico * 10000 / summary.OfficialCostPico
		summary.SavedPercentBPS = &value
	}
	if err == nil {
		_ = r.database.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE WHEN stats_disposition='included' THEN 1 ELSE 0 END),0), COALESCE(SUM(CASE WHEN stats_disposition='excluded_limit' THEN 1 ELSE 0 END),0) FROM proxy_requests`).Scan(&summary.EligibleRequests, &summary.ExcludedLimitRequests)
		if summary.EligibleRequests > 0 {
			summary.SuccessRateBPS = summary.SucceededRequests * 10000 / summary.EligibleRequests
		}
	}
	return summary, err
}

func (r *StatsRepository) ModelStats(ctx context.Context, freeModels map[string]bool) ([]ModelStats, error) {
	rows, err := r.database.QueryContext(ctx, `SELECT
		r.logical_model,
		CASE WHEN EXISTS (SELECT 1 FROM proxy_attempts pa JOIN proxy_requests rp ON rp.id = pa.request_id WHERE rp.logical_model = r.logical_model AND pa.upstream_model LIKE '%:free') THEN 1 ELSE 0 END,
		COUNT(*),
		COALESCE(SUM(CASE WHEN r.stats_disposition='included' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN r.stats_disposition='excluded_limit' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN r.state = 'succeeded' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN r.state = 'failed' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN r.state = 'partial' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(r.attempt_count), 0),
		COALESCE(SUM(CASE WHEN r.attempt_count > 1 THEN 1 ELSE 0 END), 0),
		MIN(r.duration_ms), MAX(r.duration_ms), CAST(AVG(r.duration_ms) AS INTEGER), COUNT(r.duration_ms),
		COALESCE(SUM(u.input_tokens), 0), COALESCE(SUM(u.output_tokens), 0), COALESCE(SUM(u.total_tokens), 0),
		COALESCE(SUM(u.cached_read_tokens), 0), COALESCE(SUM(u.cache_write_tokens), 0), COALESCE(SUM(u.reasoning_tokens), 0),
		COALESCE(SUM(u.estimated_cost_pico_usd), 0), COALESCE(SUM(u.official_cost_pico_usd), 0), COALESCE(SUM(u.actual_cost_pico_usd), 0),
		COALESCE(SUM(u.discount_pico_usd), 0), COALESCE(SUM(CASE WHEN u.discount_pico_usd > 0 THEN u.discount_pico_usd ELSE 0 END), 0)
		FROM proxy_requests r LEFT JOIN request_usage u ON u.request_id = r.id
		GROUP BY r.logical_model ORDER BY COUNT(*) DESC, r.logical_model`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]ModelStats, 0)
	for rows.Next() {
		var item ModelStats
		var observedFree int
		var fastest, slowest, average sql.NullInt64
		if err := rows.Scan(&item.Model, &observedFree, &item.Requests, &item.EligibleRequests, &item.ExcludedLimitRequests, &item.SucceededRequests, &item.FailedRequests, &item.PartialRequests,
			&item.TotalAttempts, &item.RetriedRequests, &fastest, &slowest, &average, &item.RequestsWithTime,
			&item.InputTokens, &item.OutputTokens, &item.TotalTokens, &item.CachedReadTokens, &item.CacheWriteTokens, &item.ReasoningTokens,
			&item.EstimatedCostPico, &item.OfficialCostPico, &item.ActualCostPico, &item.DiscountPico, &item.SavedCostPico); err != nil {
			return nil, err
		}
		item.Free = freeModels[item.Model] || observedFree != 0
		if item.Requests > 0 {
			if item.EligibleRequests > 0 {
				item.SuccessRateBPS = item.SucceededRequests * 10000 / item.EligibleRequests
			}
			item.RetryRateBPS = item.RetriedRequests * 10000 / item.Requests
		}
		if item.OfficialCostPico > 0 {
			value := item.DiscountPico * 10000 / item.OfficialCostPico
			item.DiscountBPS = &value
		}
		if fastest.Valid {
			item.FastestMS = &fastest.Int64
		}
		if slowest.Valid {
			item.SlowestMS = &slowest.Int64
		}
		if average.Valid {
			item.AverageMS = &average.Int64
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *StatsRepository) ProviderStats(ctx context.Context) ([]ProviderStats, error) {
	rows, err := r.database.QueryContext(ctx, `SELECT
		COALESCE(NULLIF(r.selected_provider, ''), 'unknown'),
		COUNT(*),
		COALESCE(SUM(CASE WHEN r.stats_disposition='included' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN r.stats_disposition='excluded_limit' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN r.state = 'succeeded' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN r.state = 'failed' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN r.state = 'partial' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(r.attempt_count), 0),
		COALESCE(SUM(CASE WHEN r.attempt_count > 1 THEN 1 ELSE 0 END), 0),
		MIN(r.duration_ms), MAX(r.duration_ms), CAST(AVG(r.duration_ms) AS INTEGER), COUNT(r.duration_ms),
		COALESCE(SUM(u.input_tokens), 0), COALESCE(SUM(u.output_tokens), 0), COALESCE(SUM(u.total_tokens), 0),
		COALESCE(SUM(u.cached_read_tokens), 0), COALESCE(SUM(u.cache_write_tokens), 0), COALESCE(SUM(u.reasoning_tokens), 0),
		COALESCE(SUM(u.estimated_cost_pico_usd), 0), COALESCE(SUM(u.official_cost_pico_usd), 0), COALESCE(SUM(u.actual_cost_pico_usd), 0),
		COALESCE(SUM(CASE WHEN u.discount_pico_usd > 0 THEN u.discount_pico_usd ELSE 0 END), 0)
		FROM proxy_requests r LEFT JOIN request_usage u ON u.request_id = r.id
		GROUP BY COALESCE(NULLIF(r.selected_provider, ''), 'unknown')
		ORDER BY COUNT(*) DESC, COALESCE(NULLIF(r.selected_provider, ''), 'unknown')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]ProviderStats, 0)
	for rows.Next() {
		var item ProviderStats
		var fastest, slowest, average sql.NullInt64
		if err := rows.Scan(&item.Provider, &item.Requests, &item.EligibleRequests, &item.ExcludedLimitRequests, &item.SucceededRequests, &item.FailedRequests, &item.PartialRequests,
			&item.TotalAttempts, &item.RetriedRequests, &fastest, &slowest, &average, &item.RequestsWithTime,
			&item.InputTokens, &item.OutputTokens, &item.TotalTokens, &item.CachedReadTokens, &item.CacheWriteTokens, &item.ReasoningTokens,
			&item.EstimatedCostPico, &item.OfficialCostPico, &item.ActualCostPico, &item.SavedCostPico); err != nil {
			return nil, err
		}
		if item.Requests > 0 {
			if item.EligibleRequests > 0 {
				item.SuccessRateBPS = item.SucceededRequests * 10000 / item.EligibleRequests
			}
			item.RetryRateBPS = item.RetriedRequests * 10000 / item.Requests
		}
		if item.OfficialCostPico > 0 {
			value := item.SavedCostPico * 10000 / item.OfficialCostPico
			if value < 0 {
				value = 0
			}
			if value > 10000 {
				value = 10000
			}
			item.DiscountBPS = &value
		}
		if fastest.Valid {
			item.FastestMS = &fastest.Int64
		}
		if slowest.Valid {
			item.SlowestMS = &slowest.Int64
		}
		if average.Valid {
			item.AverageMS = &average.Int64
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
