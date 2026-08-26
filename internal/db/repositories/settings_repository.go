package repositories

import (
	"context"
	"database/sql"
	"time"

	bobmodels "github.com/neverknowerdev/paylessforai/internal/db/bob/models"
	"github.com/stephenafamo/bob/dialect/sqlite/im"
)

type SettingsRepository struct{ bobRepository }

func (r *SettingsRepository) Get(ctx context.Context, key string) (string, bool, error) {
	setting, err := bobmodels.FindSetting(ctx, r.exec, key)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return setting.Value, true, nil
}

func (r *SettingsRepository) Set(ctx context.Context, key, value string) error {
	updatedAt := time.Now().UTC().Format(time.RFC3339Nano)
	setter := &bobmodels.SettingSetter{Key: &key, Value: &value, UpdatedAt: &updatedAt}
	_, err := bobmodels.Settings.Insert(setter, im.OnConflict("key").DoUpdate(im.SetExcluded("value", "updated_at"))).One(ctx, r.exec)
	return err
}
