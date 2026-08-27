package statserver

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type telemetryEvent struct {
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

func (s *Server) telemetry(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}
	key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if len(key) < 16 {
		http.Error(w, "installation credential required", 401)
		return
	}
	var body struct {
		BatchID string           `json:"batch_id"`
		Events  []telemetryEvent `json:"events"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	if body.BatchID == "" || len(body.Events) > 1000 {
		http.Error(w, "invalid batch", 400)
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer tx.Rollback()
	var installationID int64
	if err = tx.QueryRowContext(r.Context(), `INSERT INTO telemetry_installations(installation_key_hash,last_seen_at) VALUES($1,now()) ON CONFLICT(installation_key_hash) DO UPDATE SET last_seen_at=now() RETURNING id`, hashString(key)).Scan(&installationID); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	var batchID int64
	err = tx.QueryRowContext(r.Context(), `INSERT INTO telemetry_batches(installation_id,batch_id,event_count) VALUES($1,$2,$3) ON CONFLICT DO NOTHING RETURNING id`, installationID, body.BatchID, len(body.Events)).Scan(&batchID)
	if errors.Is(err, sql.ErrNoRows) {
		_ = tx.Commit()
		jsonResponse(w, map[string]any{"accepted": 0, "duplicate": true})
		return
	}
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	for _, e := range body.Events {
		_, err = tx.ExecContext(r.Context(), `INSERT INTO request_observations(installation_id,event_id,model_name,provider,occurred_at,total_ms,ttft_ms,generation_ms,input_tokens,output_tokens,cached_read_tokens,cache_write_tokens,cache_status,cache_ttl_seconds,observed_reuse_age_seconds,success,retry_count,cost_usd) VALUES($1,$2,$3,$4,$5,NULLIF($6,0),NULLIF($7,0),NULLIF($8,0),NULLIF($9,0),NULLIF($10,0),NULLIF($11,0),NULLIF($12,0),$13,NULLIF($14,0),NULLIF($15,0),$16,$17,$18) ON CONFLICT DO NOTHING`, installationID, e.EventID, e.ModelName, e.Provider, e.OccurredAt, e.TotalMS, e.TTFTMS, e.GenerationMS, e.InputTokens, e.OutputTokens, e.CachedReadTokens, e.CacheWriteTokens, e.CacheStatus, e.CacheTTLSeconds, e.ObservedReuseAgeSeconds, e.Success, e.RetryCount, e.CostUSD)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	if err = tx.Commit(); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	jsonResponse(w, map[string]any{"accepted": len(body.Events), "batch_id": body.BatchID})
}

func (s *Server) statistics(w http.ResponseWriter, r *http.Request) {
	model, provider := r.URL.Query().Get("model"), r.URL.Query().Get("provider")
	args := []any{}
	filters := []string{"1=1"}
	if model != "" {
		args = append(args, model)
		n := len(args)
		filters = append(filters, fmt.Sprintf("(model_name=$%d OR lower(model_name)=lower((SELECT display_name FROM models WHERE canonical_slug=$%d LIMIT 1)) OR model_id::text=$%d OR model_id=(SELECT id FROM models WHERE canonical_slug=$%d LIMIT 1))", n, n, n, n))
	}
	if provider != "" {
		args = append(args, provider)
		filters = append(filters, fmt.Sprintf("provider=$%d", len(args)))
	}
	q := `SELECT count(*),coalesce(avg(total_ms),0),coalesce(percentile_cont(0.5) WITHIN GROUP(ORDER BY total_ms),0),coalesce(avg(ttft_ms),0),coalesce(avg(generation_ms),0),coalesce(avg(input_tokens),0),coalesce(avg(output_tokens),0),coalesce(sum(CASE WHEN cache_status='hit' THEN 1 ELSE 0 END),0),coalesce(sum(CASE WHEN cache_status='miss' THEN 1 ELSE 0 END),0),coalesce(sum(CASE WHEN cache_status='write' THEN 1 ELSE 0 END),0),coalesce(sum(CASE WHEN success THEN 1 ELSE 0 END),0) FROM request_observations WHERE ` + strings.Join(filters, " AND ")
	var count, hit, miss, write, success int
	var avg, p50, ttft, generation, inTok, outTok float64
	if err := s.db.QueryRowContext(r.Context(), q, args...).Scan(&count, &avg, &p50, &ttft, &generation, &inTok, &outTok, &hit, &miss, &write, &success); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	jsonResponse(w, map[string]any{"sample_count": count, "success_count": success, "success_rate": ratio(success, count), "avg_total_ms": avg, "p50_total_ms": p50, "avg_ttft_ms": ttft, "avg_generation_ms": generation, "avg_input_tokens": inTok, "avg_output_tokens": outTok, "cache_hits": hit, "cache_misses": miss, "cache_writes": write, "cache_hit_rate": ratio(hit, hit+miss), "provider": provider, "model": model})
}

func (s *Server) modelStatistics(w http.ResponseWriter, r *http.Request, slug string) {
	q := r.URL.Query()
	q.Set("model", slug)
	r2 := r.Clone(r.Context())
	r2.URL.RawQuery = q.Encode()
	s.statistics(w, r2)
}
