package repositories

import (
	"context"
	"database/sql"
	"time"
)

type ProxyRequestsRepository struct{ db DBTX }

func (r *ProxyRequestsRepository) Create(ctx context.Context, id, clientKeyID, protocol, model string) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO proxy_requests(id,client_key_id,protocol,logical_model,state,received_at) VALUES(?,NULLIF(?,''),?,?, 'received',?)`, id, clientKeyID, protocol, model, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (r *ProxyRequestsRepository) RecordAttemptRoute(ctx context.Context, requestID string, attempt int, provider, upstream string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE proxy_requests SET selected_provider=?,selected_upstream_model=?,attempt_count=CASE WHEN attempt_count<? THEN ? ELSE attempt_count END WHERE id=?`, provider, upstream, attempt, attempt, requestID)
	return err
}

func (r *ProxyRequestsRepository) Complete(ctx context.Context, id, state, code, message string) error {
	now := time.Now().UTC()
	var received string
	if err := r.db.QueryRowContext(ctx, `SELECT received_at FROM proxy_requests WHERE id=?`, id).Scan(&received); err != nil && err != sql.ErrNoRows {
		return err
	}
	var duration any
	if parsed, e := time.Parse(time.RFC3339Nano, received); e == nil {
		d := now.Sub(parsed).Milliseconds()
		if d < 0 {
			d = 0
		}
		duration = d
	}
	disp := "included"
	if code == "provider_rate_limit" || code == "provider_quota_exhausted" || code == "all_subscription_quotas_exhausted" {
		disp = "excluded_limit"
	}
	_, err := r.db.ExecContext(ctx, `UPDATE proxy_requests SET state=?,completed_at=?,duration_ms=?,error_code=NULLIF(?,''),error_message=NULLIF(?,''),stats_disposition=CASE WHEN ?='excluded_limit' THEN 'excluded_limit' ELSE stats_disposition END WHERE id=?`, state, now.Format(time.RFC3339Nano), duration, code, message, disp, id)
	return err
}
