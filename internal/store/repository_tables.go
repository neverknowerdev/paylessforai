package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/neverknowerdev/paylessforai/internal/ids"
	"github.com/neverknowerdev/paylessforai/internal/retry"
	"github.com/neverknowerdev/paylessforai/internal/subscription"
)

type SettingsRepository struct{ db DBTX }

func (r *SettingsRepository) Get(ctx context.Context, key string) (string, bool, error) {
	var value string
	err := r.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	return value, err == nil, err
}
func (r *SettingsRepository) Set(ctx context.Context, key, value string) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO settings(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, key, value, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

type ClientAPIKeysRepository struct{ db DBTX }

func (r *ClientAPIKeysRepository) Create(ctx context.Context, label string) (ClientKey, string, error) {
	if strings.TrimSpace(label) == "" {
		label = "default"
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return ClientKey{}, "", err
	}
	secret := "plai_" + hex.EncodeToString(b)
	h := sha256.Sum256([]byte(secret))
	id := ids.New()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	prefix := secret[:13]
	_, err := r.db.ExecContext(ctx, `INSERT INTO client_api_keys(id,label,key_hash,key_prefix,created_at) VALUES(?,?,?,?,?)`, id, label, hex.EncodeToString(h[:]), prefix, now)
	return ClientKey{ID: id, Label: label, Prefix: prefix, CreatedAt: now}, secret, err
}
func (r *ClientAPIKeysRepository) Authenticate(ctx context.Context, secret string) (ClientKey, bool, error) {
	h := sha256.Sum256([]byte(secret))
	var k ClientKey
	var revoked sql.NullString
	err := r.db.QueryRowContext(ctx, `SELECT id,label,key_prefix,created_at,last_used_at,revoked_at FROM client_api_keys WHERE key_hash=?`, hex.EncodeToString(h[:])).Scan(&k.ID, &k.Label, &k.Prefix, &k.CreatedAt, &k.LastUsedAt, &revoked)
	if err == sql.ErrNoRows {
		return ClientKey{}, false, nil
	}
	if err != nil {
		return ClientKey{}, false, err
	}
	if revoked.Valid {
		return k, false, nil
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, _ = r.db.ExecContext(ctx, `UPDATE client_api_keys SET last_used_at=? WHERE id=?`, now, k.ID)
	k.LastUsedAt = &now
	return k, true, nil
}
func (r *ClientAPIKeysRepository) List(ctx context.Context) ([]ClientKey, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,label,key_prefix,created_at,last_used_at,revoked_at FROM client_api_keys ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ClientKey{}
	for rows.Next() {
		var k ClientKey
		if err := rows.Scan(&k.ID, &k.Label, &k.Prefix, &k.CreatedAt, &k.LastUsedAt, &k.RevokedAt); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}
func (r *ClientAPIKeysRepository) Revoke(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE client_api_keys SET revoked_at=? WHERE id=? AND revoked_at IS NULL`, time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}

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
func (r *ProviderCredentialsRepository) Usage(ctx context.Context) ([]subscription.UsageRow, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT c.provider,c.label,c.subscription_fee_pico_usd,c.subscription_cycle_start,c.subscription_cycle_end,r.received_at,COALESCE(u.input_tokens,0),COALESCE(u.output_tokens,0) FROM provider_credentials c JOIN proxy_requests r ON r.selected_provider=c.provider LEFT JOIN request_usage u ON u.request_id=r.id WHERE c.access_mode='subscription' AND c.subscription_fee_pico_usd IS NOT NULL AND r.state='succeeded' ORDER BY r.received_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []subscription.UsageRow{}
	for rows.Next() {
		var p, l, at string
		var fee int64
		var start, end sql.NullString
		var in, outTokens int64
		if err := rows.Scan(&p, &l, &fee, &start, &end, &at, &in, &outTokens); err != nil {
			return nil, err
		}
		when, e := time.Parse(time.RFC3339Nano, at)
		if e != nil {
			continue
		}
		u := subscription.UsageRow{Provider: p, Label: l, FeePicoUSD: fee, At: when, InputTokens: in, OutputTokens: outTokens}
		if start.Valid {
			u.CycleStart, _ = time.Parse(time.RFC3339Nano, start.String)
		}
		if end.Valid {
			u.CycleEnd, _ = time.Parse(time.RFC3339Nano, end.String)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

type ProxyRequestsRepository struct{ db DBTX }

func (r *ProxyRequestsRepository) Create(ctx context.Context, id, clientKeyID, protocol, model string) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO proxy_requests(id,client_key_id,protocol,logical_model,state,received_at) VALUES(?,NULLIF(?,''),?,?, 'received',?)`, id, clientKeyID, protocol, model, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}
func (r *ProxyRequestsRepository) Complete(ctx context.Context, id, state, code, message string) error {
	now := time.Now().UTC()
	var received string
	if err := r.db.QueryRowContext(ctx, `SELECT received_at FROM proxy_requests WHERE id=?`, id).Scan(&received); err != nil && err != sql.ErrNoRows {
		return err
	}
	var duration any
	if parsed, e := time.Parse(time.RFC3339Nano, received); e == nil {
		d := now.Sub(parsed).Milliseconds()
		if d < 0 {
			d = 0
		}
		duration = d
	}
	disp := "included"
	if code == "provider_rate_limit" || code == "provider_quota_exhausted" || code == "all_subscription_quotas_exhausted" {
		disp = "excluded_limit"
	}
	_, err := r.db.ExecContext(ctx, `UPDATE proxy_requests SET state=?,completed_at=?,duration_ms=?,error_code=NULLIF(?,''),error_message=NULLIF(?,''),stats_disposition=CASE WHEN ?='excluded_limit' THEN 'excluded_limit' ELSE stats_disposition END WHERE id=?`, state, now.Format(time.RFC3339Nano), duration, code, message, disp, id)
	return err
}

type ProxyAttemptsRepository struct{ db DBTX }

func (r *ProxyAttemptsRepository) Record(ctx context.Context, requestID string, attempt int, provider, upstream, state, errorClass, errorMessage string, rawError ...string) error {
	if attempt < 1 {
		return fmt.Errorf("attempt number must be positive")
	}
	now := time.Now().UTC()
	raw := ""
	if len(rawError) > 0 {
		raw = rawError[0]
	}
	var started string
	if err := r.db.QueryRowContext(ctx, `SELECT started_at FROM proxy_attempts WHERE id=?`, requestID+":"+fmt.Sprint(attempt)).Scan(&started); err != nil && err != sql.ErrNoRows {
		return err
	}
	var duration any
	if state != "started" {
		if parsed, e := time.Parse(time.RFC3339Nano, started); e == nil {
			duration = now.Sub(parsed).Milliseconds()
		} else {
			duration = int64(0)
		}
	}
	disp := "included"
	if errorClass == string(retry.ErrorRateLimit) || errorClass == string(retry.ErrorQuotaExhausted) {
		disp = "excluded_limit"
	}
	_, err := r.db.ExecContext(ctx, `UPDATE proxy_requests SET selected_provider=?,selected_upstream_model=?,attempt_count=CASE WHEN attempt_count<? THEN ? ELSE attempt_count END WHERE id=?`, provider, upstream, attempt, attempt, requestID)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO proxy_attempts(id,request_id,attempt_number,route_id,provider,upstream_model,state,started_at,completed_at,duration_ms,error_class,error_message,error_raw,stats_disposition) VALUES(?,?,?,?,?,?,?, ?,NULLIF(?,''),?,NULLIF(?,''),NULLIF(?,''),NULLIF(?,''),?) ON CONFLICT(id) DO UPDATE SET provider=excluded.provider,upstream_model=excluded.upstream_model,state=excluded.state,completed_at=excluded.completed_at,duration_ms=COALESCE(excluded.duration_ms,proxy_attempts.duration_ms),error_class=excluded.error_class,error_message=excluded.error_message,error_raw=excluded.error_raw,stats_disposition=excluded.stats_disposition`, requestID+":"+fmt.Sprint(attempt), requestID, attempt, provider+":"+upstream, provider, upstream, state, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), duration, errorClass, errorMessage, raw, disp)
	return err
}

type RequestUsageRepository struct{ db DBTX }

func (r *RequestUsageRepository) Upsert(ctx context.Context, u RequestUsage) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO request_usage(request_id,input_tokens,output_tokens,total_tokens,cached_read_tokens,cache_write_tokens,reasoning_tokens,estimated_cost_pico_usd,official_cost_pico_usd,actual_cost_pico_usd,discount_pico_usd,discount_percent_bps,raw_usage_json) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(request_id) DO UPDATE SET input_tokens=excluded.input_tokens,output_tokens=excluded.output_tokens,total_tokens=excluded.total_tokens,cached_read_tokens=excluded.cached_read_tokens,cache_write_tokens=excluded.cache_write_tokens,reasoning_tokens=excluded.reasoning_tokens,estimated_cost_pico_usd=excluded.estimated_cost_pico_usd,official_cost_pico_usd=excluded.official_cost_pico_usd,actual_cost_pico_usd=excluded.actual_cost_pico_usd,discount_pico_usd=excluded.discount_pico_usd,discount_percent_bps=excluded.discount_percent_bps,raw_usage_json=excluded.raw_usage_json`, u.RequestID, u.InputTokens, u.OutputTokens, u.TotalTokens, u.CachedReadTokens, u.CacheWriteTokens, u.ReasoningTokens, u.EstimatedCostPico, u.OfficialCostPico, u.ActualCostPico, u.DiscountPico, u.DiscountBPS, u.RawUsageJSON)
	return err
}

// The remaining repositories provide table-scoped access for catalog state.
// They intentionally keep their SQL small; higher-level refresh aggregation
// remains in the catalog service.
type CatalogRefreshesRepository struct{ db DBTX }
type CatalogRefresh struct {
	ID, Provider, Status, StartedAt string
	CompletedAt, Error              *string
}

func (r *CatalogRefreshesRepository) Create(ctx context.Context, v CatalogRefresh) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO catalog_refreshes(id,provider,status,started_at,completed_at,error) VALUES(?,?,?,?,?,?)`, v.ID, v.Provider, v.Status, v.StartedAt, v.CompletedAt, v.Error)
	return err
}

type ModelsRepository struct{ db DBTX }

type ModelRecord struct {
	ID              string
	DisplayName     string
	ContextLength   int64
	MaxOutputTokens int64
	MetadataJSON    string
	ObservedAt      string
}

func (r *ModelsRepository) Upsert(ctx context.Context, model ModelRecord) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO models(id,display_name,context_length,max_output_tokens,metadata_json,observed_at) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET display_name=excluded.display_name,context_length=excluded.context_length,max_output_tokens=excluded.max_output_tokens,metadata_json=excluded.metadata_json,observed_at=excluded.observed_at`, model.ID, model.DisplayName, model.ContextLength, model.MaxOutputTokens, model.MetadataJSON, model.ObservedAt)
	return err
}

func (r *ModelsRepository) Get(ctx context.Context, id string) (ModelRecord, error) {
	var model ModelRecord
	err := r.db.QueryRowContext(ctx, `SELECT id,display_name,context_length,max_output_tokens,metadata_json,observed_at FROM models WHERE id=?`, id).Scan(&model.ID, &model.DisplayName, &model.ContextLength, &model.MaxOutputTokens, &model.MetadataJSON, &model.ObservedAt)
	return model, err
}

func (r *ModelsRepository) DeleteAll(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM models`)
	return err
}

type ModelRoutesRepository struct{ db DBTX }

type ModelRouteRecord struct {
	ID, ModelID, Provider, UpstreamModel, Protocol, PriceJSON, CapabilitiesJSON, Health, ObservedAt string
	Trusted                                                                                         bool
}

func (r *ModelRoutesRepository) Upsert(ctx context.Context, route ModelRouteRecord) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO model_routes(id,model_id,provider,upstream_model,protocol,price_json,capabilities_json,health,trusted,observed_at) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET model_id=excluded.model_id,provider=excluded.provider,upstream_model=excluded.upstream_model,protocol=excluded.protocol,price_json=excluded.price_json,capabilities_json=excluded.capabilities_json,health=excluded.health,trusted=excluded.trusted,observed_at=excluded.observed_at`, route.ID, route.ModelID, route.Provider, route.UpstreamModel, route.Protocol, route.PriceJSON, route.CapabilitiesJSON, route.Health, boolInt(route.Trusted), route.ObservedAt)
	return err
}

func (r *ModelRoutesRepository) Get(ctx context.Context, id string) (ModelRouteRecord, error) {
	var route ModelRouteRecord
	var trusted int
	err := r.db.QueryRowContext(ctx, `SELECT id,model_id,provider,upstream_model,protocol,price_json,capabilities_json,health,trusted,observed_at FROM model_routes WHERE id=?`, id).Scan(&route.ID, &route.ModelID, &route.Provider, &route.UpstreamModel, &route.Protocol, &route.PriceJSON, &route.CapabilitiesJSON, &route.Health, &trusted, &route.ObservedAt)
	route.Trusted = trusted != 0
	return route, err
}

func (r *ModelRoutesRepository) DeleteAll(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM model_routes`)
	return err
}

type ProviderHealthRepository struct{ db DBTX }

type ProviderHealthRecord struct {
	RouteID      string
	FailureCount int64
	State        string
	BackoffUntil *string
	LastError    *string
	UpdatedAt    string
}

func (r *ProviderHealthRepository) Upsert(ctx context.Context, health ProviderHealthRecord) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO provider_health(route_id,failure_count,state,backoff_until,last_error,updated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(route_id) DO UPDATE SET failure_count=excluded.failure_count,state=excluded.state,backoff_until=excluded.backoff_until,last_error=excluded.last_error,updated_at=excluded.updated_at`, health.RouteID, health.FailureCount, health.State, health.BackoffUntil, health.LastError, health.UpdatedAt)
	return err
}

func (r *ProviderHealthRepository) Get(ctx context.Context, routeID string) (ProviderHealthRecord, error) {
	var health ProviderHealthRecord
	err := r.db.QueryRowContext(ctx, `SELECT route_id,failure_count,state,backoff_until,last_error,updated_at FROM provider_health WHERE route_id=?`, routeID).Scan(&health.RouteID, &health.FailureCount, &health.State, &health.BackoffUntil, &health.LastError, &health.UpdatedAt)
	return health, err
}

func (r *ProviderHealthRepository) DeleteAll(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM provider_health`)
	return err
}
