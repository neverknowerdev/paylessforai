package repositories

import (
	"context"
	"database/sql"
	"time"

	bobmodels "github.com/neverknowerdev/paylessforai/internal/db/bob/models"
	"github.com/neverknowerdev/paylessforai/internal/db/models"
	"github.com/stephenafamo/bob/dialect/sqlite"
	"github.com/stephenafamo/bob/dialect/sqlite/im"
	"github.com/stephenafamo/bob/dialect/sqlite/sm"
	"github.com/stephenafamo/bob/dialect/sqlite/um"
)

type ProviderCredentialsRepository struct{ bobRepository }

func (r *ProviderCredentialsRepository) Upsert(ctx context.Context, c models.ProviderCredential) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if c.CreatedAt == "" {
		c.CreatedAt = now
	}
	if c.AccessMode == "" {
		c.AccessMode = "api"
	}
	if c.SubscriptionStatus == "" {
		c.SubscriptionStatus = "available"
	}
	lastChecked := nullableString(c.LastCheckedAt)
	lastError := nullableString(c.LastError)
	fee := nullableInt64(c.SubscriptionFeePicoUSD)
	cycleStart := nullableString(c.SubscriptionCycleStart)
	cycleEnd := nullableString(c.SubscriptionCycleEnd)
	nextAvailable := nullableString(c.NextAvailableAt)
	statusReason := nullableString(c.StatusReason)
	enabled := boolInt(c.Enabled)
	setter := &bobmodels.ProviderCredentialSetter{ID: &c.ID, Provider: &c.Provider, Label: &c.Label, BaseURL: &c.BaseURL, Ciphertext: &c.Ciphertext, Nonce: &c.Nonce, Enabled: &enabled, CreatedAt: &c.CreatedAt, UpdatedAt: &now, LastCheckedAt: &lastChecked, LastError: &lastError, ManualModelsJSON: &c.ManualModelsJSON, AccessMode: &c.AccessMode, SubscriptionFeePicoUsd: &fee, SubscriptionCycleStart: &cycleStart, SubscriptionCycleEnd: &cycleEnd, SubscriptionStatus: &c.SubscriptionStatus, NextAvailableAt: &nextAvailable, StatusReason: &statusReason}
	_, err := bobmodels.ProviderCredentials.Insert(setter, im.OnConflict("id").DoUpdate(im.SetExcluded("provider", "label", "base_url", "ciphertext", "nonce", "enabled", "updated_at", "manual_models_json", "access_mode", "subscription_fee_pico_usd", "subscription_cycle_start", "subscription_cycle_end", "subscription_status", "next_available_at", "status_reason"))).One(ctx, r.exec)
	return err
}
func (r *ProviderCredentialsRepository) List(ctx context.Context) ([]models.ProviderCredential, error) {
	rows, err := bobmodels.ProviderCredentials.Query(sm.OrderBy(bobmodels.ProviderCredentials.Columns.CreatedAt).Desc()).All(ctx, r.exec)
	if err != nil {
		return nil, err
	}
	out := make([]models.ProviderCredential, 0, len(rows))
	for _, row := range rows {
		out = append(out, providerCredentialFromBob(row))
	}
	return out, nil
}
func (r *ProviderCredentialsRepository) Delete(ctx context.Context, id string) error {
	row, err := bobmodels.FindProviderCredential(ctx, r.exec, id)
	if err != nil {
		return err
	}
	return row.Delete(ctx, r.exec)
}
func (r *ProviderCredentialsRepository) MarkLimited(ctx context.Context, provider string, next *time.Time, reason string) error {
	var n *string
	if next != nil {
		value := next.UTC().Format(time.RFC3339Nano)
		n = &value
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	limited := "limited"
	reasonValue := nullableString(pointerIfNonEmpty(reason))
	nextValue := nullableString(n)
	lastChecked := nullableString(&now)
	setter := &bobmodels.ProviderCredentialSetter{SubscriptionStatus: &limited, NextAvailableAt: &nextValue, StatusReason: &reasonValue, LastError: &reasonValue, LastCheckedAt: &lastChecked, UpdatedAt: &now}
	_, err := bobmodels.ProviderCredentials.Update(setter.UpdateMod(), um.Where(bobmodels.ProviderCredentials.Columns.Provider.EQ(sqlite.Arg(provider)))).Exec(ctx, r.exec)
	return err
}
func (r *ProviderCredentialsRepository) ClearExpired(ctx context.Context, now time.Time) error {
	available := "available"
	empty := sql.Null[string]{}
	nowValue := now.UTC().Format(time.RFC3339Nano)
	setter := &bobmodels.ProviderCredentialSetter{SubscriptionStatus: &available, NextAvailableAt: &empty, StatusReason: &empty}
	_, err := bobmodels.ProviderCredentials.Update(setter.UpdateMod(), um.Where(sqlite.And(bobmodels.ProviderCredentials.Columns.SubscriptionStatus.EQ(sqlite.Arg("limited")), bobmodels.ProviderCredentials.Columns.NextAvailableAt.IsNotNull(), bobmodels.ProviderCredentials.Columns.NextAvailableAt.LTE(sqlite.Arg(nowValue))))).Exec(ctx, r.exec)
	return err
}

func providerCredentialFromBob(row *bobmodels.ProviderCredential) models.ProviderCredential {
	return models.ProviderCredential{ID: row.ID, Provider: row.Provider, Label: row.Label, BaseURL: row.BaseURL, Ciphertext: row.Ciphertext, Nonce: row.Nonce, Enabled: row.Enabled != 0, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, LastCheckedAt: stringPointer(row.LastCheckedAt), LastError: stringPointer(row.LastError), ManualModelsJSON: row.ManualModelsJSON, AccessMode: row.AccessMode, SubscriptionFeePicoUSD: int64Pointer(row.SubscriptionFeePicoUsd), SubscriptionCycleStart: stringPointer(row.SubscriptionCycleStart), SubscriptionCycleEnd: stringPointer(row.SubscriptionCycleEnd), SubscriptionStatus: row.SubscriptionStatus, NextAvailableAt: stringPointer(row.NextAvailableAt), StatusReason: stringPointer(row.StatusReason)}
}
