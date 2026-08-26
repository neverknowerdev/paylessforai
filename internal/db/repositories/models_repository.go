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
	bobRepository
}

func (r *ModelsRepository) Upsert(ctx context.Context, m ModelRecord) error {
	setter := &bobmodels.ModelSetter{ID: &m.ID, DisplayName: &m.DisplayName, ContextLength: &m.ContextLength, MaxOutputTokens: &m.MaxOutputTokens, MetadataJSON: &m.MetadataJSON, ObservedAt: &m.ObservedAt}
	_, err := bobmodels.Models.Insert(setter, im.OnConflict("id").DoUpdate(im.SetExcluded("display_name", "context_length", "max_output_tokens", "metadata_json", "observed_at"))).One(ctx, r.exec)
	return err
}
func (r *ModelsRepository) Get(ctx context.Context, id string) (ModelRecord, error) {
	row, err := bobmodels.FindModel(ctx, r.exec, id)
	if err != nil {
		return ModelRecord{}, err
	}
	return ModelRecord{ID: row.ID, DisplayName: row.DisplayName, ContextLength: row.ContextLength, MaxOutputTokens: row.MaxOutputTokens, MetadataJSON: row.MetadataJSON, ObservedAt: row.ObservedAt}, nil
}
func (r *ModelsRepository) DeleteAll(ctx context.Context) error {
	_, err := bob.Exec(ctx, r.exec, sqlite.Delete(dm.From(bobmodels.Models.NameAsExpr())))
	return err
}
