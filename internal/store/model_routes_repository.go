package store

import "context"

type ModelRoutesRepository struct{ db DBTX }
type ModelRouteRecord struct {
	ID, ModelID, Provider, UpstreamModel, Protocol, PriceJSON, CapabilitiesJSON, Health, ObservedAt string
	Trusted                                                                                         bool
}

func (r *ModelRoutesRepository) Upsert(ctx context.Context, v ModelRouteRecord) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO model_routes(id,model_id,provider,upstream_model,protocol,price_json,capabilities_json,health,trusted,observed_at) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET model_id=excluded.model_id,provider=excluded.provider,upstream_model=excluded.upstream_model,protocol=excluded.protocol,price_json=excluded.price_json,capabilities_json=excluded.capabilities_json,health=excluded.health,trusted=excluded.trusted,observed_at=excluded.observed_at`, v.ID, v.ModelID, v.Provider, v.UpstreamModel, v.Protocol, v.PriceJSON, v.CapabilitiesJSON, v.Health, boolInt(v.Trusted), v.ObservedAt)
	return err
}
func (r *ModelRoutesRepository) Get(ctx context.Context, id string) (ModelRouteRecord, error) {
	var v ModelRouteRecord
	var trusted int
	err := r.db.QueryRowContext(ctx, `SELECT id,model_id,provider,upstream_model,protocol,price_json,capabilities_json,health,trusted,observed_at FROM model_routes WHERE id=?`, id).Scan(&v.ID, &v.ModelID, &v.Provider, &v.UpstreamModel, &v.Protocol, &v.PriceJSON, &v.CapabilitiesJSON, &v.Health, &trusted, &v.ObservedAt)
	v.Trusted = trusted != 0
	return v, err
}
func (r *ModelRoutesRepository) DeleteAll(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM model_routes`)
	return err
}
