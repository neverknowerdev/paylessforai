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

type ModelRoutesRepository struct{ bobRepository }

func (r *ModelRoutesRepository) Upsert(ctx context.Context, v models.ModelRouteRecord) error {
	trusted := boolInt(v.Trusted)
	setter := &bobmodels.ModelRouteSetter{ID: &v.ID, ModelID: &v.ModelID, Provider: &v.Provider, UpstreamModel: &v.UpstreamModel, Protocol: &v.Protocol, PriceJSON: &v.PriceJSON, CapabilitiesJSON: &v.CapabilitiesJSON, Health: &v.Health, Trusted: &trusted, ObservedAt: &v.ObservedAt}
	_, err := bobmodels.ModelRoutes.Insert(setter, im.OnConflict("id").DoUpdate(im.SetExcluded("model_id", "provider", "upstream_model", "protocol", "price_json", "capabilities_json", "health", "trusted", "observed_at"))).One(ctx, r.exec)
	return err
}

func (r *ModelRoutesRepository) Get(ctx context.Context, id string) (models.ModelRouteRecord, error) {
	row, err := bobmodels.FindModelRoute(ctx, r.exec, id)
	if err != nil {
		return models.ModelRouteRecord{}, err
	}
	return models.ModelRouteRecord{ID: row.ID, ModelID: row.ModelID, Provider: row.Provider, UpstreamModel: row.UpstreamModel, Protocol: row.Protocol, PriceJSON: row.PriceJSON, CapabilitiesJSON: row.CapabilitiesJSON, Health: row.Health, Trusted: row.Trusted != 0, ObservedAt: row.ObservedAt}, nil
}

func (r *ModelRoutesRepository) DeleteAll(ctx context.Context) error {
	_, err := bob.Exec(ctx, r.exec, sqlite.Delete(dm.From(bobmodels.ModelRoutes.NameAsExpr())))
	return err
}
