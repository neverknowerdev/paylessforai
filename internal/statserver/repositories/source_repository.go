package repositories

import (
	"context"
	"database/sql"
	"time"

	"github.com/neverknowerdev/paylessforai/internal/statserver/models"
)

type SourceRepository struct{ db *sql.DB }

func (r *SourceRepository) StartRefresh(ctx context.Context, source models.Source) (int64, error) {
	var id int64
	err := r.db.QueryRowContext(ctx, `INSERT INTO sources(key,display_name,base_url,last_attempt_at,updated_at) VALUES($1,$2,$3,$4,now()) ON CONFLICT(key) DO UPDATE SET last_attempt_at=EXCLUDED.last_attempt_at,base_url=EXCLUDED.base_url,updated_at=now() RETURNING id`, source.Key, source.DisplayName, source.BaseURL, time.Now().UTC()).Scan(&id)
	return id, err
}

func (r *SourceRepository) RecordFailure(ctx context.Context, sourceID int64, refreshErr error) error {
	_, err := r.db.ExecContext(ctx, `UPDATE sources SET last_error=$1,updated_at=now() WHERE id=$2`, refreshErr.Error(), sourceID)
	return err
}

func (r *SourceRepository) RecordSnapshot(ctx context.Context, sourceID int64, contentHash string, payload []byte) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO source_snapshots(source_id,content_hash,payload) VALUES($1,$2,$3)`, sourceID, contentHash, payload)
	return err
}

func (r *SourceRepository) RecordSuccess(ctx context.Context, sourceID int64, count int) error {
	_, err := r.db.ExecContext(ctx, `UPDATE sources SET last_success_at=now(),last_error=NULL,record_count=$1,updated_at=now() WHERE id=$2`, count, sourceID)
	return err
}

func (r *SourceRepository) List(ctx context.Context) ([]models.Source, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT key,display_name,base_url,last_attempt_at,last_success_at,last_error,record_count FROM sources ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []models.Source{}
	for rows.Next() {
		var item models.Source
		var attempt, success sql.NullTime
		var lastErr sql.NullString
		if err := rows.Scan(&item.Key, &item.DisplayName, &item.BaseURL, &attempt, &success, &lastErr, &item.RecordCount); err != nil {
			return nil, err
		}
		if attempt.Valid {
			item.LastAttemptAt = &attempt.Time
		}
		if success.Valid {
			item.LastSuccessAt = &success.Time
		}
		item.LastError = lastErr.String
		items = append(items, item)
	}
	return items, rows.Err()
}
