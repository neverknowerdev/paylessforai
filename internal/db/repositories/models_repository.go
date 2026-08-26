package repositories

import "context"

type ModelsRepository struct{ db DBTX }

func (r *ModelsRepository) Upsert(ctx context.Context, m ModelRecord) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO models(id,display_name,context_length,max_output_tokens,metadata_json,observed_at) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET display_name=excluded.display_name,context_length=excluded.context_length,max_output_tokens=excluded.max_output_tokens,metadata_json=excluded.metadata_json,observed_at=excluded.observed_at`, m.ID, m.DisplayName, m.ContextLength, m.MaxOutputTokens, m.MetadataJSON, m.ObservedAt)
	return err
}
func (r *ModelsRepository) Get(ctx context.Context, id string) (ModelRecord, error) {
	var m ModelRecord
	err := r.db.QueryRowContext(ctx, `SELECT id,display_name,context_length,max_output_tokens,metadata_json,observed_at FROM models WHERE id=?`, id).Scan(&m.ID, &m.DisplayName, &m.ContextLength, &m.MaxOutputTokens, &m.MetadataJSON, &m.ObservedAt)
	return m, err
}
func (r *ModelsRepository) DeleteAll(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM models`)
	return err
}
