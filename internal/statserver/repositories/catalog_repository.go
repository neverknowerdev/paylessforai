package repositories

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/neverknowerdev/paylessforai/internal/statserver/models"
)

type CatalogRepository struct{ db *sql.DB }

func (r *CatalogRepository) Count(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, `SELECT count(*) FROM models`).Scan(&count)
	return count, err
}

func (r *CatalogRepository) List(ctx context.Context, limit int) ([]models.ModelSummary, int, error) {
	total, err := r.Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT m.id,m.canonical_slug,m.display_name,m.creator,m.family,m.revision,m.description,m.context_length,m.updated_at,(SELECT count(*) FROM provider_offerings o WHERE o.model_id=m.id AND o.status='active'),(SELECT count(*) FROM benchmark_results b WHERE b.model_id=m.id) FROM models m ORDER BY m.display_name LIMIT $1`, limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]models.ModelSummary, 0)
	for rows.Next() {
		item, err := scanSummary(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}

func (r *CatalogRepository) Search(ctx context.Context, query string) ([]models.SearchResult, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT m.id,m.canonical_slug,m.display_name,m.creator,m.updated_at,(SELECT count(*) FROM provider_offerings o WHERE o.model_id=m.id),(SELECT count(*) FROM benchmark_results b WHERE b.model_id=m.id),CASE WHEN m.normalized_name=$1 THEN 0 WHEN m.normalized_name LIKE $1||'%' THEN 1 ELSE 2 END FROM models m WHERE m.normalized_name=$1 OR m.normalized_name LIKE $1||'%' OR m.normalized_name ILIKE '%'||$1||'%' OR EXISTS(SELECT 1 FROM model_aliases a WHERE a.model_id=m.id AND a.normalized_alias ILIKE '%'||$1||'%') ORDER BY 8,m.display_name LIMIT 100`, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]models.SearchResult, 0)
	for rows.Next() {
		var item models.SearchResult
		var rank int
		if err := rows.Scan(&item.ID, &item.CanonicalSlug, &item.DisplayName, &item.Creator, &item.UpdatedAt, &item.OfferingCount, &item.BenchmarkCount, &rank); err != nil {
			return nil, err
		}
		item.MatchType = []string{"exact", "prefix", "contains"}[min(rank, 2)]
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *CatalogRepository) Resolve(ctx context.Context, name string) (models.ModelSummary, error) {
	var item models.ModelSummary
	err := r.db.QueryRowContext(ctx, `SELECT id,canonical_slug,display_name,creator FROM models WHERE normalized_name=$1 OR id IN(SELECT model_id FROM model_aliases WHERE normalized_alias=$1 AND (valid_until IS NULL OR valid_until>now())) ORDER BY id LIMIT 1`, name).Scan(&item.ID, &item.CanonicalSlug, &item.DisplayName, &item.Creator)
	return item, err
}

func (r *CatalogRepository) Detail(ctx context.Context, slug string) (models.ModelDetail, error) {
	var item models.ModelDetail
	var contextLength sql.NullInt64
	err := r.db.QueryRowContext(ctx, `SELECT id,canonical_slug,display_name,creator,family,revision,description,context_length,updated_at FROM models WHERE canonical_slug=$1 OR id::text=$1 LIMIT 1`, slug).Scan(&item.ID, &item.CanonicalSlug, &item.DisplayName, &item.Creator, &item.Family, &item.Revision, &item.Description, &contextLength, &item.UpdatedAt)
	if err != nil {
		return item, err
	}
	if contextLength.Valid {
		item.ContextLength = &contextLength.Int64
	}
	offers, err := r.db.QueryContext(ctx, `SELECT provider,provider_model_id,variant,input_usd_per_million,output_usd_per_million,cache_read_usd_per_million,cache_write_usd_per_million,status,observed_at FROM provider_offerings WHERE model_id=$1 ORDER BY provider,provider_model_id`, item.ID)
	if err != nil {
		return item, err
	}
	defer offers.Close()
	for offers.Next() {
		var offering models.Offering
		var in, out, read, write sql.NullFloat64
		if err := offers.Scan(&offering.Provider, &offering.ProviderModelID, &offering.Variant, &in, &out, &read, &write, &offering.Status, &offering.ObservedAt); err != nil {
			return item, err
		}
		offering.InputUSDPerMillion, offering.OutputUSDPerMillion = nullableFloat(in), nullableFloat(out)
		offering.CacheReadUSDPerMillion, offering.CacheWriteUSDPerMillion = nullableFloat(read), nullableFloat(write)
		item.Offerings = append(item.Offerings, offering)
	}
	if err := offers.Err(); err != nil {
		return item, err
	}
	benchmarks, err := r.db.QueryContext(ctx, `SELECT benchmark_name,version,metric,value,unit,verified,source_key,observed_at FROM benchmark_results WHERE model_id=$1 ORDER BY normalized_name,observed_at DESC`, item.ID)
	if err != nil {
		return item, err
	}
	defer benchmarks.Close()
	for benchmarks.Next() {
		var benchmark models.BenchmarkResult
		if err := benchmarks.Scan(&benchmark.Name, &benchmark.Version, &benchmark.Metric, &benchmark.Value, &benchmark.Unit, &benchmark.Verified, &benchmark.Source, &benchmark.ObservedAt); err != nil {
			return item, err
		}
		item.Benchmarks = append(item.Benchmarks, benchmark)
	}
	return item, benchmarks.Err()
}

func (r *CatalogRepository) ModelID(ctx context.Context, slug string) (int64, error) {
	var id int64
	err := r.db.QueryRowContext(ctx, `SELECT id FROM models WHERE canonical_slug=$1 OR id::text=$1 LIMIT 1`, slug).Scan(&id)
	return id, err
}

func (r *CatalogRepository) ModelIDs(ctx context.Context) ([]int64, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id FROM models`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (r *CatalogRepository) LatestBenchmark(ctx context.Context, modelID int64, selector string) (float64, error) {
	var value float64
	err := r.db.QueryRowContext(ctx, `SELECT value FROM benchmark_results WHERE model_id=$1 AND normalized_name=$2 ORDER BY verified DESC,observed_at DESC,id DESC LIMIT 1`, modelID, models.NormalizeBenchmark(selector)).Scan(&value)
	return value, err
}

func (r *CatalogRepository) UpsertRecord(ctx context.Context, source string, record models.CatalogRecord) (int64, error) {
	identity := record.Name
	if record.Creator != "" {
		prefix := strings.ToLower(strings.TrimSpace(record.Creator)) + ":"
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(identity)), prefix) {
			identity = strings.TrimSpace(identity[len(prefix):])
		}
	}
	normalized := models.Normalize(identity)
	slug := models.CanonicalSlug(record.Creator, record.Name, record.Revision)
	var modelID int64
	err := r.db.QueryRowContext(ctx, `SELECT id FROM models WHERE normalized_name=$1 AND (lower(creator)=lower($2) OR creator='' OR $2='') ORDER BY id LIMIT 1`, normalized, record.Creator).Scan(&modelID)
	if errors.Is(err, sql.ErrNoRows) {
		err = r.db.QueryRowContext(ctx, `INSERT INTO models(canonical_slug,display_name,normalized_name,creator,family,revision,description,context_length,source_key,source_id,metadata,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,0),$9,$10,$11,now()) ON CONFLICT(source_key,source_id) DO UPDATE SET display_name=EXCLUDED.display_name,normalized_name=EXCLUDED.normalized_name,creator=EXCLUDED.creator,family=EXCLUDED.family,revision=EXCLUDED.revision,description=EXCLUDED.description,context_length=EXCLUDED.context_length,metadata=EXCLUDED.metadata,updated_at=now() RETURNING id`, slug, record.Name, normalized, record.Creator, record.Family, record.Revision, record.Description, record.Context, source, record.SourceID, jsonBytes(record.Metadata)).Scan(&modelID)
	}
	if err != nil {
		return 0, fmt.Errorf("upsert model: %w", err)
	}
	if _, err = r.db.ExecContext(ctx, `INSERT INTO model_aliases(model_id,alias,normalized_alias,source_key,evidence) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, modelID, record.Name, normalized, source, jsonBytes(map[string]any{"source_id": record.SourceID})); err != nil {
		return 0, err
	}
	if record.ProviderModel != "" {
		if _, err = r.db.ExecContext(ctx, `INSERT INTO provider_offerings(model_id,provider,provider_model_id,input_usd_per_million,output_usd_per_million,cache_read_usd_per_million,cache_write_usd_per_million,context_length,metadata,observed_at) VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,0),$9,now()) ON CONFLICT(provider,provider_model_id) DO UPDATE SET model_id=EXCLUDED.model_id,input_usd_per_million=EXCLUDED.input_usd_per_million,output_usd_per_million=EXCLUDED.output_usd_per_million,cache_read_usd_per_million=EXCLUDED.cache_read_usd_per_million,cache_write_usd_per_million=EXCLUDED.cache_write_usd_per_million,context_length=EXCLUDED.context_length,metadata=EXCLUDED.metadata,observed_at=now(),status='active'`, modelID, source, record.ProviderModel, record.Input, record.Output, record.CacheRead, record.CacheWrite, record.Context, jsonBytes(record.Metadata)); err != nil {
			return 0, err
		}
	}
	for name, value := range record.Benchmarks {
		if _, err = r.db.ExecContext(ctx, `INSERT INTO benchmark_results(model_id,benchmark_name,normalized_name,value,unit,source_key,observed_at) VALUES($1,$2,$3,$4,'fraction',$5,now())`, modelID, name, models.NormalizeBenchmark(name), value, source); err != nil {
			return 0, err
		}
	}
	return modelID, nil
}

type scanner interface{ Scan(...any) error }

func scanSummary(row scanner) (models.ModelSummary, error) {
	var item models.ModelSummary
	var contextLength sql.NullInt64
	err := row.Scan(&item.ID, &item.CanonicalSlug, &item.DisplayName, &item.Creator, &item.Family, &item.Revision, &item.Description, &contextLength, &item.UpdatedAt, &item.OfferingCount, &item.BenchmarkCount)
	if contextLength.Valid {
		item.ContextLength = &contextLength.Int64
	}
	return item, err
}
func nullableFloat(value sql.NullFloat64) *float64 {
	if value.Valid {
		return &value.Float64
	}
	return nil
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
