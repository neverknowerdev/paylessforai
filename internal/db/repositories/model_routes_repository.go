package repositories

import (
	"context"
	"database/sql"

	bobmodels "github.com/neverknowerdev/paylessforai/internal/db/bob/models"
	"github.com/neverknowerdev/paylessforai/internal/db/models"
	"github.com/neverknowerdev/paylessforai/internal/wire"
	"github.com/stephenafamo/bob"
	"github.com/stephenafamo/bob/dialect/sqlite"
	"github.com/stephenafamo/bob/dialect/sqlite/dm"
	"github.com/stephenafamo/bob/dialect/sqlite/im"
)

type ModelRoutesRepository struct{ bobRepository }

func (r *ModelRoutesRepository) Upsert(ctx context.Context, v models.ModelRouteRecord) error {
	if v.Format == "" {
		if existing, err := r.Get(ctx, v.ID); err == nil {
			v.Format = existing.Format
		}
	}
	trusted := boolInt(v.Trusted)
	format := nullableStringPointer(pointerIfNonEmpty(v.Format))
	setter := &bobmodels.ModelRouteSetter{ID: &v.ID, ModelID: &v.ModelID, Provider: &v.Provider, UpstreamModel: &v.UpstreamModel, Format: format, PriceJSON: &v.PriceJSON, CapabilitiesJSON: &v.CapabilitiesJSON, Health: &v.Health, Trusted: &trusted, ObservedAt: &v.ObservedAt}
	_, err := bobmodels.ModelRoutes.Insert(setter, im.OnConflict("id").DoUpdate(im.SetExcluded("model_id", "provider", "upstream_model", "format", "price_json", "capabilities_json", "health", "trusted", "observed_at"))).One(ctx, r.exec)
	return err
}

func (r *ModelRoutesRepository) Get(ctx context.Context, id string) (models.ModelRouteRecord, error) {
	row, err := bobmodels.FindModelRoute(ctx, r.exec, id)
	if err != nil {
		return models.ModelRouteRecord{}, err
	}
	return models.ModelRouteRecord{ID: row.ID, ModelID: row.ModelID, Provider: row.Provider, UpstreamModel: row.UpstreamModel, Format: stringValue(row.Format), PriceJSON: row.PriceJSON, CapabilitiesJSON: row.CapabilitiesJSON, Health: row.Health, Trusted: row.Trusted != 0, ObservedAt: row.ObservedAt}, nil
}

func (r *ModelRoutesRepository) GetFormat(ctx context.Context, routeID string) (wire.Format, bool, error) {
	row, err := bobmodels.FindModelRoute(ctx, r.exec, routeID)
	if err != nil {
		return wire.FormatUnknown, false, err
	}
	if !row.Format.Valid || row.Format.V == "" {
		return wire.FormatUnknown, false, nil
	}
	format := wire.Format(row.Format.V)
	if !format.Valid() {
		return wire.FormatUnknown, false, nil
	}
	return format, true, nil
}

func (r *ModelRoutesRepository) SetFormat(ctx context.Context, routeID string, format wire.Format) error {
	if err := format.Validate(); err != nil {
		return err
	}
	row, err := bobmodels.FindModelRoute(ctx, r.exec, routeID)
	if err != nil {
		return err
	}
	value := sql.Null[string]{V: string(format), Valid: true}
	return row.Update(ctx, r.exec, &bobmodels.ModelRouteSetter{Format: &value})
}

func (r *ModelRoutesRepository) ClearFormat(ctx context.Context, routeID string) error {
	row, err := bobmodels.FindModelRoute(ctx, r.exec, routeID)
	if err != nil {
		return err
	}
	value := sql.Null[string]{}
	return row.Update(ctx, r.exec, &bobmodels.ModelRouteSetter{Format: &value})
}

func (r *ModelRoutesRepository) DeleteAll(ctx context.Context) error {
	_, err := bob.Exec(ctx, r.exec, sqlite.Delete(dm.From(bobmodels.ModelRoutes.NameAsExpr())))
	return err
}
