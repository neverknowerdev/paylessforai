package store

import "context"

type ProviderHealthRepository struct{ db DBTX }
type ProviderHealthRecord struct {
	RouteID                 string
	FailureCount            int64
	State                   string
	BackoffUntil, LastError *string
	UpdatedAt               string
}

func (r *ProviderHealthRepository) Upsert(ctx context.Context, v ProviderHealthRecord) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO provider_health(route_id,failure_count,state,backoff_until,last_error,updated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(route_id) DO UPDATE SET failure_count=excluded.failure_count,state=excluded.state,backoff_until=excluded.backoff_until,last_error=excluded.last_error,updated_at=excluded.updated_at`, v.RouteID, v.FailureCount, v.State, v.BackoffUntil, v.LastError, v.UpdatedAt)
	return err
}
func (r *ProviderHealthRepository) Get(ctx context.Context, id string) (ProviderHealthRecord, error) {
	var v ProviderHealthRecord
	err := r.db.QueryRowContext(ctx, `SELECT route_id,failure_count,state,backoff_until,last_error,updated_at FROM provider_health WHERE route_id=?`, id).Scan(&v.RouteID, &v.FailureCount, &v.State, &v.BackoffUntil, &v.LastError, &v.UpdatedAt)
	return v, err
}
func (r *ProviderHealthRepository) DeleteAll(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM provider_health`)
	return err
}
