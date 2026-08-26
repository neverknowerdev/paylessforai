package repositories

import (
	"context"

	bobmodels "github.com/neverknowerdev/paylessforai/internal/db/bob/models"
	"github.com/stephenafamo/bob"
	"github.com/stephenafamo/bob/dialect/sqlite"
	"github.com/stephenafamo/bob/dialect/sqlite/dm"
	"github.com/stephenafamo/bob/dialect/sqlite/im"
)

type ModelsRepository struct {
	db  DBTX
	bob bob.Executor
}

func (r *ModelsRepository) Upsert(ctx context.Context, m ModelRecord) error {
	if r.bob != nil {
		setter := &bobmodels.ModelSetter{
			ID:              &m.ID,
			DisplayName:     &m.DisplayName,
			ContextLength:   &m.ContextLength,
			MaxOutputTokens: &m.MaxOutputTokens,
			MetadataJSON:    &m.MetadataJSON,
			ObservedAt:      &m.ObservedAt,
		}
		_, err := bobmodels.Models.Insert(
			setter,
			im.OnConflict("id").DoUpdate(
				im.SetExcluded("display_name", "context_length", "max_output_tokens", "metadata_json", "observed_at"),
			),
		).One(ctx, r.bob)
		return err
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO models(id,display_name,context_length,max_output_tokens,metadata_json,observed_at) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET display_name=excluded.display_name,context_length=excluded.context_length,max_output_tokens=excluded.max_output_tokens,metadata_json=excluded.metadata_json,observed_at=excluded.observed_at`, m.ID, m.DisplayName, m.ContextLength, m.MaxOutputTokens, m.MetadataJSON, m.ObservedAt)
	return err
}
func (r *ModelsRepository) Get(ctx context.Context, id string) (ModelRecord, error) {
	if r.bob != nil {
		row, err := bobmodels.FindModel(ctx, r.bob, id)
		if err != nil {
			return ModelRecord{}, err
		}
		return ModelRecord{
			ID:              row.ID,
			DisplayName:     row.DisplayName,
			ContextLength:   row.ContextLength,
			MaxOutputTokens: row.MaxOutputTokens,
			MetadataJSON:    row.MetadataJSON,
			ObservedAt:      row.ObservedAt,
		}, nil
	}
	var m ModelRecord
	err := r.db.QueryRowContext(ctx, `SELECT id,display_name,context_length,max_output_tokens,metadata_json,observed_at FROM models WHERE id=?`, id).Scan(&m.ID, &m.DisplayName, &m.ContextLength, &m.MaxOutputTokens, &m.MetadataJSON, &m.ObservedAt)
	return m, err
}
func (r *ModelsRepository) DeleteAll(ctx context.Context) error {
	if r.bob != nil {
		_, err := bob.Exec(ctx, r.bob, sqlite.Delete(dm.From(bobmodels.Models.NameAsExpr())))
		return err
	}
	_, err := r.db.ExecContext(ctx, `DELETE FROM models`)
	return err
}
