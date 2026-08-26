package repositories

import (
	"context"
	"time"
)

type ProviderCredentialsRepository struct{ db DBTX }

func (r *ProviderCredentialsRepository) Upsert(ctx context.Context, c ProviderCredential) error {
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
	_, err := r.db.ExecContext(ctx, `INSERT INTO provider_credentials(id,provider,label,base_url,ciphertext,nonce,enabled,created_at,updated_at,manual_models_json,access_mode,subscription_fee_pico_usd,subscription_cycle_start,subscription_cycle_end,subscription_status,next_available_at,status_reason) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET provider=excluded.provider,label=excluded.label,base_url=excluded.base_url,ciphertext=excluded.ciphertext,nonce=excluded.nonce,enabled=excluded.enabled,updated_at=excluded.updated_at,manual_models_json=excluded.manual_models_json,access_mode=excluded.access_mode,subscription_fee_pico_usd=excluded.subscription_fee_pico_usd,subscription_cycle_start=excluded.subscription_cycle_start,subscription_cycle_end=excluded.subscription_cycle_end,subscription_status=excluded.subscription_status,next_available_at=excluded.next_available_at,status_reason=excluded.status_reason`, c.ID, c.Provider, c.Label, c.BaseURL, c.Ciphertext, c.Nonce, boolInt(c.Enabled), c.CreatedAt, now, c.ManualModelsJSON, c.AccessMode, c.SubscriptionFeePicoUSD, c.SubscriptionCycleStart, c.SubscriptionCycleEnd, c.SubscriptionStatus, c.NextAvailableAt, c.StatusReason)
	return err
}
func (r *ProviderCredentialsRepository) List(ctx context.Context) ([]ProviderCredential, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,provider,label,base_url,ciphertext,nonce,enabled,created_at,updated_at,last_checked_at,last_error,manual_models_json,access_mode,subscription_fee_pico_usd,subscription_cycle_start,subscription_cycle_end,subscription_status,next_available_at,status_reason FROM provider_credentials ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProviderCredential{}
	for rows.Next() {
		var c ProviderCredential
		var enabled int
		if err := rows.Scan(&c.ID, &c.Provider, &c.Label, &c.BaseURL, &c.Ciphertext, &c.Nonce, &enabled, &c.CreatedAt, &c.UpdatedAt, &c.LastCheckedAt, &c.LastError, &c.ManualModelsJSON, &c.AccessMode, &c.SubscriptionFeePicoUSD, &c.SubscriptionCycleStart, &c.SubscriptionCycleEnd, &c.SubscriptionStatus, &c.NextAvailableAt, &c.StatusReason); err != nil {
			return nil, err
		}
		c.Enabled = enabled != 0
		out = append(out, c)
	}
	return out, rows.Err()
}
func (r *ProviderCredentialsRepository) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM provider_credentials WHERE id=?`, id)
	return err
}
func (r *ProviderCredentialsRepository) MarkLimited(ctx context.Context, provider string, next *time.Time, reason string) error {
	var n any
	if next != nil {
		n = next.UTC().Format(time.RFC3339Nano)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := r.db.ExecContext(ctx, `UPDATE provider_credentials SET subscription_status='limited',next_available_at=?,status_reason=?,last_error=?,last_checked_at=?,updated_at=? WHERE provider=?`, n, NULLString(reason), NULLString(reason), now, now, provider)
	return err
}
func (r *ProviderCredentialsRepository) ClearExpired(ctx context.Context, now time.Time) error {
	_, err := r.db.ExecContext(ctx, `UPDATE provider_credentials SET subscription_status='available',next_available_at=NULL,status_reason=NULL WHERE subscription_status='limited' AND next_available_at IS NOT NULL AND next_available_at<=?`, now.UTC().Format(time.RFC3339Nano))
	return err
}
