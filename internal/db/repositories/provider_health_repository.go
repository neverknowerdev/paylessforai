package repositories

import (
	"context"

	bobmodels "github.com/neverknowerdev/paylessforai/internal/db/bob/models"
	"github.com/neverknowerdev/paylessforai/internal/db/models"
	"github.com/stephenafamo/bob"
	"github.com/stephenafamo/bob/dialect/sqlite"
	"github.com/stephenafamo/bob/dialect/sqlite/dm"
	"github.com/stephenafamo/bob/dialect/sqlite/im"
)

type ProviderHealthRepository struct{ bobRepository }

func (r *ProviderHealthRepository) Upsert(ctx context.Context, v models.ProviderHealthRecord) error {
	backoff := nullableString(v.BackoffUntil)
	lastError := nullableString(v.LastError)
	setter := &bobmodels.ProviderHealthSetter{RouteID: &v.RouteID, FailureCount: &v.FailureCount, State: &v.State, BackoffUntil: &backoff, LastError: &lastError, UpdatedAt: &v.UpdatedAt}
	_, err := bobmodels.ProviderHealths.Insert(setter, im.OnConflict("route_id").DoUpdate(im.SetExcluded("failure_count", "state", "backoff_until", "last_error", "updated_at"))).One(ctx, r.exec)
	return err
}

func (r *ProviderHealthRepository) Get(ctx context.Context, id string) (models.ProviderHealthRecord, error) {
	row, err := bobmodels.FindProviderHealth(ctx, r.exec, id)
	if err != nil {
		return models.ProviderHealthRecord{}, err
	}
	return models.ProviderHealthRecord{RouteID: row.RouteID, FailureCount: row.FailureCount, State: row.State, BackoffUntil: stringPointer(row.BackoffUntil), LastError: stringPointer(row.LastError), UpdatedAt: row.UpdatedAt}, nil
}

func (r *ProviderHealthRepository) DeleteAll(ctx context.Context) error {
	_, err := bob.Exec(ctx, r.exec, sqlite.Delete(dm.From(bobmodels.ProviderHealths.NameAsExpr())))
	return err
}
