package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/neverknowerdev/paylessforai/internal/retry"
)

type ProxyAttemptsRepository struct{ db DBTX }

func (r *ProxyAttemptsRepository) Record(ctx context.Context, requestID string, attempt int, provider, upstream, state, errorClass, errorMessage string, rawError ...string) error {
	if attempt < 1 {
		return fmt.Errorf("attempt number must be positive")
	}
	now := time.Now().UTC()
	raw := ""
	if len(rawError) > 0 {
		raw = rawError[0]
	}
	var started string
	if err := r.db.QueryRowContext(ctx, `SELECT started_at FROM proxy_attempts WHERE id=?`, requestID+":"+fmt.Sprint(attempt)).Scan(&started); err != nil && err != sql.ErrNoRows {
		return err
	}
	var duration any
	if state != "started" {
		if parsed, e := time.Parse(time.RFC3339Nano, started); e == nil {
			duration = now.Sub(parsed).Milliseconds()
		} else {
			duration = int64(0)
		}
	}
	disp := "included"
	if errorClass == string(retry.ErrorRateLimit) || errorClass == string(retry.ErrorQuotaExhausted) {
		disp = "excluded_limit"
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO proxy_attempts(id,request_id,attempt_number,route_id,provider,upstream_model,state,started_at,completed_at,duration_ms,error_class,error_message,error_raw,stats_disposition) VALUES(?,?,?,?,?,?,?, ?,NULLIF(?,''),?,NULLIF(?,''),NULLIF(?,''),NULLIF(?,''),?) ON CONFLICT(id) DO UPDATE SET provider=excluded.provider,upstream_model=excluded.upstream_model,state=excluded.state,completed_at=excluded.completed_at,duration_ms=COALESCE(excluded.duration_ms,proxy_attempts.duration_ms),error_class=excluded.error_class,error_message=excluded.error_message,error_raw=excluded.error_raw,stats_disposition=excluded.stats_disposition`, requestID+":"+fmt.Sprint(attempt), requestID, attempt, provider+":"+upstream, provider, upstream, state, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), duration, errorClass, errorMessage, raw, disp)
	return err
}
