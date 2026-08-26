package repositories

import "context"

import (
	bobmodels "github.com/neverknowerdev/paylessforai/internal/db/bob/models"
)

type CatalogRefreshesRepository struct{ bobRepository }

func (r *CatalogRefreshesRepository) Create(ctx context.Context, v CatalogRefresh) error {
	completedAt := nullableString(v.CompletedAt)
	errorValue := nullableString(v.Error)
	setter := &bobmodels.CatalogRefreshSetter{ID: &v.ID, Provider: &v.Provider, Status: &v.Status, StartedAt: &v.StartedAt, CompletedAt: &completedAt, Error: &errorValue}
	_, err := bobmodels.CatalogRefreshes.Insert(setter).One(ctx, r.exec)
	return err
}
