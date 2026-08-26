package store

import "context"

type CatalogRefreshesRepository struct{ db DBTX }
type CatalogRefresh struct {
	ID, Provider, Status, StartedAt string
	CompletedAt, Error              *string
}

func (r *CatalogRefreshesRepository) Create(ctx context.Context, v CatalogRefresh) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO catalog_refreshes(id,provider,status,started_at,completed_at,error) VALUES(?,?,?,?,?,?)`, v.ID, v.Provider, v.Status, v.StartedAt, v.CompletedAt, v.Error)
	return err
}
