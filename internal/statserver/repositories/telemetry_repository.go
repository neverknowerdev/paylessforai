package repositories

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/neverknowerdev/paylessforai/internal/statserver/models"
)

type TelemetryRepository struct{ db *sql.DB }

func (r *TelemetryRepository) Ingest(ctx context.Context, installationKeyHash string, batch models.TelemetryBatch) (accepted int, duplicate bool, err error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()
	var installationID int64
	if err = tx.QueryRowContext(ctx, `INSERT INTO telemetry_installations(installation_key_hash,last_seen_at) VALUES($1,now()) ON CONFLICT(installation_key_hash) DO UPDATE SET last_seen_at=now() RETURNING id`, installationKeyHash).Scan(&installationID); err != nil {
		return 0, false, err
	}
	var batchRecordID int64
	err = tx.QueryRowContext(ctx, `INSERT INTO telemetry_batches(installation_id,batch_id,event_count) VALUES($1,$2,$3) ON CONFLICT DO NOTHING RETURNING id`, installationID, batch.BatchID, len(batch.Events)).Scan(&batchRecordID)
	if errors.Is(err, sql.ErrNoRows) {
		if err = tx.Commit(); err != nil {
			return 0, false, err
		}
		return 0, true, nil
	}
	if err != nil {
		return 0, false, err
	}
	for _, event := range batch.Events {
		_, err = tx.ExecContext(ctx, `INSERT INTO request_observations(installation_id,event_id,model_name,provider,occurred_at,total_ms,ttft_ms,generation_ms,input_tokens,output_tokens,cached_read_tokens,cache_write_tokens,cache_status,cache_ttl_seconds,observed_reuse_age_seconds,success,retry_count,cost_usd) VALUES($1,$2,$3,$4,$5,NULLIF($6,0),NULLIF($7,0),NULLIF($8,0),NULLIF($9,0),NULLIF($10,0),NULLIF($11,0),NULLIF($12,0),$13,NULLIF($14,0),NULLIF($15,0),$16,$17,$18) ON CONFLICT DO NOTHING`, installationID, event.EventID, event.ModelName, event.Provider, event.OccurredAt, event.TotalMS, event.TTFTMS, event.GenerationMS, event.InputTokens, event.OutputTokens, event.CachedReadTokens, event.CacheWriteTokens, event.CacheStatus, event.CacheTTLSeconds, event.ObservedReuseAgeSeconds, event.Success, event.RetryCount, event.CostUSD)
		if err != nil {
			return 0, false, err
		}
	}
	if err = tx.Commit(); err != nil {
		return 0, false, err
	}
	return len(batch.Events), false, nil
}

func (r *TelemetryRepository) Statistics(ctx context.Context, model, provider string) (models.Statistics, error) {
	result := models.Statistics{Model: model, Provider: provider}
	args, filters := []any{}, []string{"1=1"}
	if model != "" {
		args = append(args, model)
		index := len(args)
		filters = append(filters, fmt.Sprintf("(model_name=$%d OR lower(model_name)=lower((SELECT display_name FROM models WHERE canonical_slug=$%d LIMIT 1)) OR model_id::text=$%d OR model_id=(SELECT id FROM models WHERE canonical_slug=$%d LIMIT 1))", index, index, index, index))
	}
	if provider != "" {
		args = append(args, provider)
		filters = append(filters, fmt.Sprintf("provider=$%d", len(args)))
	}
	query := `SELECT count(*),coalesce(avg(total_ms),0),coalesce(percentile_cont(0.5) WITHIN GROUP(ORDER BY total_ms),0),coalesce(avg(ttft_ms),0),coalesce(avg(generation_ms),0),coalesce(avg(input_tokens),0),coalesce(avg(output_tokens),0),coalesce(sum(CASE WHEN cache_status='hit' THEN 1 ELSE 0 END),0),coalesce(sum(CASE WHEN cache_status='miss' THEN 1 ELSE 0 END),0),coalesce(sum(CASE WHEN cache_status='write' THEN 1 ELSE 0 END),0),coalesce(sum(CASE WHEN success THEN 1 ELSE 0 END),0) FROM request_observations WHERE ` + strings.Join(filters, " AND ")
	err := r.db.QueryRowContext(ctx, query, args...).Scan(&result.SampleCount, &result.AverageTotalMS, &result.P50TotalMS, &result.AverageTTFTMS, &result.AverageGenerationMS, &result.AverageInputTokens, &result.AverageOutputTokens, &result.CacheHits, &result.CacheMisses, &result.CacheWrites, &result.SuccessCount)
	if err != nil {
		return result, err
	}
	result.SuccessRate = ratio(result.SuccessCount, result.SampleCount)
	result.CacheHitRate = ratio(result.CacheHits, result.CacheHits+result.CacheMisses)
	return result, nil
}

func ratio(numerator, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}
