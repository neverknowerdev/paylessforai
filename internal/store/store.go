package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"database/sql"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type Store struct {
	db *sql.DB
}

type ClientKey struct {
	ID         string  `json:"id"`
	Label      string  `json:"label"`
	Prefix     string  `json:"prefix"`
	CreatedAt  string  `json:"created_at"`
	LastUsedAt *string `json:"last_used_at,omitempty"`
	RevokedAt  *string `json:"revoked_at,omitempty"`
}

type RequestUsage struct {
	RequestID         string
	InputTokens       int64
	OutputTokens      int64
	TotalTokens       int64
	CachedReadTokens  int64
	CacheWriteTokens  int64
	ReasoningTokens   int64
	EstimatedCostPico int64
	OfficialCostPico  int64
	ActualCostPico    *int64
	DiscountPico      *int64
	DiscountBPS       *int64
	RawUsageJSON      string
}

type AttemptStat struct {
	Number        int64  `json:"number"`
	Provider      string `json:"provider"`
	UpstreamModel string `json:"upstream_model"`
	State         string `json:"state"`
	StartedAt     string `json:"started_at"`
	CompletedAt   string `json:"completed_at,omitempty"`
	ErrorClass    string `json:"error_class,omitempty"`
	ErrorMessage  string `json:"error_message,omitempty"`
	RawError      string `json:"raw_error,omitempty"`
}

type RequestStat struct {
	ID                string        `json:"id"`
	Protocol          string        `json:"protocol"`
	Model             string        `json:"model"`
	State             string        `json:"state"`
	ReceivedAt        string        `json:"received_at"`
	CompletedAt       *string       `json:"completed_at,omitempty"`
	DurationMS        *int64        `json:"duration_ms,omitempty"`
	ErrorCode         *string       `json:"error_code,omitempty"`
	InputTokens       int64         `json:"input_tokens"`
	OutputTokens      int64         `json:"output_tokens"`
	TotalTokens       int64         `json:"total_tokens"`
	CachedReadTokens  int64         `json:"cached_read_tokens"`
	CacheWriteTokens  int64         `json:"cache_write_tokens"`
	ReasoningTokens   int64         `json:"reasoning_tokens"`
	EstimatedCostPico int64         `json:"estimated_cost_pico_usd"`
	OfficialCostPico  *int64        `json:"official_cost_pico_usd,omitempty"`
	ActualCostPico    *int64        `json:"actual_cost_pico_usd,omitempty"`
	DiscountPico      *int64        `json:"discount_pico_usd,omitempty"`
	DiscountBPS       *int64        `json:"discount_percent_bps,omitempty"`
	Provider          string        `json:"provider,omitempty"`
	UpstreamModel     string        `json:"upstream_model,omitempty"`
	Attempts          int64         `json:"attempts"`
	AttemptDetails    []AttemptStat `json:"attempt_details,omitempty"`
}

type StatsSummary struct {
	TotalRequests      int64  `json:"total_requests"`
	SucceededRequests  int64  `json:"succeeded_requests"`
	FailedRequests     int64  `json:"failed_requests"`
	PartialRequests    int64  `json:"partial_requests"`
	InputTokens        int64  `json:"input_tokens"`
	OutputTokens       int64  `json:"output_tokens"`
	TotalTokens        int64  `json:"total_tokens"`
	CachedReadTokens   int64  `json:"cached_read_tokens"`
	CacheWriteTokens   int64  `json:"cache_write_tokens"`
	ReasoningTokens    int64  `json:"reasoning_tokens"`
	EstimatedCostPico  int64  `json:"estimated_cost_pico_usd"`
	ActualCostPico     int64  `json:"actual_cost_pico_usd"`
	SavedCostPico      int64  `json:"saved_cost_pico_usd"`
	RequestsWithActual int64  `json:"requests_with_actual_cost"`
	TotalAttempts      int64  `json:"total_attempts"`
	RetriedRequests    int64  `json:"retried_requests"`
	FastestMS          *int64 `json:"fastest_response_ms,omitempty"`
	SlowestMS          *int64 `json:"slowest_response_ms,omitempty"`
	AverageMS          *int64 `json:"average_response_ms,omitempty"`
	RequestsWithTime   int64  `json:"requests_with_response_time"`
}

type ModelStats struct {
	Model             string `json:"model"`
	Free              bool   `json:"free"`
	Requests          int64  `json:"requests"`
	SucceededRequests int64  `json:"succeeded_requests"`
	FailedRequests    int64  `json:"failed_requests"`
	PartialRequests   int64  `json:"partial_requests"`
	SuccessRateBPS    int64  `json:"success_rate_bps"`
	TotalAttempts     int64  `json:"total_attempts"`
	RetriedRequests   int64  `json:"retried_requests"`
	RetryRateBPS      int64  `json:"retry_rate_bps"`
	FastestMS         *int64 `json:"fastest_response_ms,omitempty"`
	SlowestMS         *int64 `json:"slowest_response_ms,omitempty"`
	AverageMS         *int64 `json:"average_response_ms,omitempty"`
	RequestsWithTime  int64  `json:"requests_with_response_time"`
	InputTokens       int64  `json:"input_tokens"`
	OutputTokens      int64  `json:"output_tokens"`
	TotalTokens       int64  `json:"total_tokens"`
	CachedReadTokens  int64  `json:"cached_read_tokens"`
	CacheWriteTokens  int64  `json:"cache_write_tokens"`
	ReasoningTokens   int64  `json:"reasoning_tokens"`
	EstimatedCostPico int64  `json:"estimated_cost_pico_usd"`
	OfficialCostPico  int64  `json:"official_cost_pico_usd"`
	ActualCostPico    int64  `json:"actual_cost_pico_usd"`
	SavedCostPico     int64  `json:"saved_cost_pico_usd"`
	DiscountPico      int64  `json:"discount_pico_usd"`
	DiscountBPS       *int64 `json:"discount_percent_bps,omitempty"`
}

type ProviderCredential struct {
	ID            string  `json:"id"`
	Provider      string  `json:"provider"`
	Label         string  `json:"label"`
	Ciphertext    []byte  `json:"-"`
	Nonce         []byte  `json:"-"`
	Enabled       bool    `json:"enabled"`
	CreatedAt     string  `json:"created_at"`
	UpdatedAt     string  `json:"updated_at"`
	LastCheckedAt *string `json:"last_checked_at,omitempty"`
	LastError     *string `json:"last_error,omitempty"`
}

func Open(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("database path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	store := &Store{db: db}
	if err := store.configure(ctx); err != nil {
		db.Close()
		return nil, err
	}
	if err := store.Migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) configure(ctx context.Context) error {
	for _, statement := range []string{"PRAGMA foreign_keys = ON", "PRAGMA journal_mode = WAL", "PRAGMA busy_timeout = 5000"} {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("configure sqlite: %s: %w", statement, err)
		}
	}
	return nil
}

func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) CreateClientKey(ctx context.Context, label string) (ClientKey, string, error) {
	if strings.TrimSpace(label) == "" {
		label = "default"
	}
	secretBytes := make([]byte, 32)
	if _, err := rand.Read(secretBytes); err != nil {
		return ClientKey{}, "", fmt.Errorf("generate client key: %w", err)
	}
	secret := "plai_" + hex.EncodeToString(secretBytes)
	hash := sha256.Sum256([]byte(secret))
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return ClientKey{}, "", fmt.Errorf("generate client key ID: %w", err)
	}
	id := hex.EncodeToString(idBytes)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO client_api_keys(id, label, key_hash, key_prefix, created_at) VALUES(?, ?, ?, ?, ?)`, id, label, hex.EncodeToString(hash[:]), secret[:13], now); err != nil {
		return ClientKey{}, "", fmt.Errorf("store client key: %w", err)
	}
	return ClientKey{ID: id, Label: label, Prefix: secret[:13], CreatedAt: now}, secret, nil
}

func (s *Store) AuthenticateClientKey(ctx context.Context, secret string) (ClientKey, bool, error) {
	hash := sha256.Sum256([]byte(secret))
	var key ClientKey
	var lastUsed, revoked *string
	err := s.db.QueryRowContext(ctx, `SELECT id, label, key_prefix, created_at, last_used_at, revoked_at FROM client_api_keys WHERE key_hash = ?`, hex.EncodeToString(hash[:])).Scan(&key.ID, &key.Label, &key.Prefix, &key.CreatedAt, &lastUsed, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return ClientKey{}, false, nil
	}
	if err != nil {
		return ClientKey{}, false, err
	}
	key.LastUsedAt, key.RevokedAt = lastUsed, revoked
	if revoked != nil {
		return key, false, nil
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.db.ExecContext(ctx, `UPDATE client_api_keys SET last_used_at = ? WHERE id = ?`, now, key.ID); err != nil {
		return ClientKey{}, false, err
	}
	key.LastUsedAt = &now
	return key, true, nil
}

func (s *Store) ListClientKeys(ctx context.Context) ([]ClientKey, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, label, key_prefix, created_at, last_used_at, revoked_at FROM client_api_keys ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []ClientKey
	for rows.Next() {
		var key ClientKey
		if err := rows.Scan(&key.ID, &key.Label, &key.Prefix, &key.CreatedAt, &key.LastUsedAt, &key.RevokedAt); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

func (s *Store) RevokeClientKey(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE client_api_keys SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}

func (s *Store) UpsertProviderCredential(ctx context.Context, credential ProviderCredential) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if credential.CreatedAt == "" {
		credential.CreatedAt = now
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO provider_credentials(id, provider, label, ciphertext, nonce, enabled, created_at, updated_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO UPDATE SET provider=excluded.provider, label=excluded.label, ciphertext=excluded.ciphertext, nonce=excluded.nonce, enabled=excluded.enabled, updated_at=excluded.updated_at`, credential.ID, credential.Provider, credential.Label, credential.Ciphertext, credential.Nonce, boolInt(credential.Enabled), credential.CreatedAt, now)
	return err
}

func (s *Store) ListProviderCredentials(ctx context.Context) ([]ProviderCredential, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, provider, label, ciphertext, nonce, enabled, created_at, updated_at, last_checked_at, last_error FROM provider_credentials ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []ProviderCredential
	for rows.Next() {
		var credential ProviderCredential
		var enabled int
		if err := rows.Scan(&credential.ID, &credential.Provider, &credential.Label, &credential.Ciphertext, &credential.Nonce, &enabled, &credential.CreatedAt, &credential.UpdatedAt, &credential.LastCheckedAt, &credential.LastError); err != nil {
			return nil, err
		}
		credential.Enabled = enabled != 0
		result = append(result, credential)
	}
	return result, rows.Err()
}

func (s *Store) DeleteProviderCredential(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM provider_credentials WHERE id = ?`, id)
	return err
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (s *Store) CreateProxyRequest(ctx context.Context, id, clientKeyID, protocol, model string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO proxy_requests(id, client_key_id, protocol, logical_model, state, received_at) VALUES(?, NULLIF(?, ''), ?, ?, 'received', ?)`, id, clientKeyID, protocol, model, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) CompleteProxyRequest(ctx context.Context, id, state, errorCode, errorMessage string) error {
	now := time.Now().UTC()
	completedAt := now.Format(time.RFC3339Nano)
	var receivedAt string
	if err := s.db.QueryRowContext(ctx, `SELECT received_at FROM proxy_requests WHERE id = ?`, id).Scan(&receivedAt); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var duration any
	if parsed, err := time.Parse(time.RFC3339Nano, receivedAt); err == nil {
		ms := now.Sub(parsed).Milliseconds()
		if ms < 0 {
			ms = 0
		}
		duration = ms
	}
	_, err := s.db.ExecContext(ctx, `UPDATE proxy_requests SET state = ?, completed_at = ?, duration_ms = ?, error_code = NULLIF(?, ''), error_message = NULLIF(?, '') WHERE id = ?`, state, completedAt, duration, errorCode, errorMessage, id)
	return err
}

// RecordProxyAttempt records both the route actually contacted and the durable
// attempt count used by the request UI and postmortem diagnostics.
func (s *Store) RecordProxyAttempt(ctx context.Context, requestID string, attempt int, provider, upstream, state, errorClass, errorMessage string, rawError ...string) error {
	if attempt < 1 {
		return fmt.Errorf("attempt number must be positive")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.db.ExecContext(ctx, `UPDATE proxy_requests SET selected_provider = ?, selected_upstream_model = ?, attempt_count = CASE WHEN attempt_count < ? THEN ? ELSE attempt_count END WHERE id = ?`, provider, upstream, attempt, attempt, requestID); err != nil {
		return err
	}
	raw := ""
	if len(rawError) > 0 {
		raw = rawError[0]
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO proxy_attempts(id, request_id, attempt_number, route_id, provider, upstream_model, state, started_at, completed_at, error_class, error_message, error_raw) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, '')) ON CONFLICT(id) DO UPDATE SET provider=excluded.provider, upstream_model=excluded.upstream_model, state=excluded.state, completed_at=excluded.completed_at, error_class=excluded.error_class, error_message=excluded.error_message, error_raw=excluded.error_raw`, requestID+":"+fmt.Sprint(attempt), requestID, attempt, provider+":"+upstream, provider, upstream, state, now, now, errorClass, errorMessage, raw)
	return err
}

func (s *Store) RecordUsage(ctx context.Context, usage RequestUsage) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO request_usage(request_id, input_tokens, output_tokens, total_tokens, cached_read_tokens, cache_write_tokens, reasoning_tokens, estimated_cost_pico_usd, official_cost_pico_usd, actual_cost_pico_usd, discount_pico_usd, discount_percent_bps, raw_usage_json) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(request_id) DO UPDATE SET input_tokens=excluded.input_tokens, output_tokens=excluded.output_tokens, total_tokens=excluded.total_tokens, cached_read_tokens=excluded.cached_read_tokens, cache_write_tokens=excluded.cache_write_tokens, reasoning_tokens=excluded.reasoning_tokens, estimated_cost_pico_usd=excluded.estimated_cost_pico_usd, official_cost_pico_usd=excluded.official_cost_pico_usd, actual_cost_pico_usd=excluded.actual_cost_pico_usd, discount_pico_usd=excluded.discount_pico_usd, discount_percent_bps=excluded.discount_percent_bps, raw_usage_json=excluded.raw_usage_json`, usage.RequestID, usage.InputTokens, usage.OutputTokens, usage.TotalTokens, usage.CachedReadTokens, usage.CacheWriteTokens, usage.ReasoningTokens, usage.EstimatedCostPico, usage.OfficialCostPico, usage.ActualCostPico, usage.DiscountPico, usage.DiscountBPS, usage.RawUsageJSON)
	return err
}

func (s *Store) ListRequestStats(ctx context.Context, limit int) ([]RequestStat, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT r.id, r.protocol, r.logical_model, r.state, r.received_at, r.completed_at, r.duration_ms, r.error_code,
		COALESCE(r.selected_provider, ''), COALESCE(r.selected_upstream_model, ''), r.attempt_count,
		u.input_tokens, u.output_tokens, u.total_tokens, u.cached_read_tokens, u.cache_write_tokens, u.reasoning_tokens,
		u.estimated_cost_pico_usd, u.official_cost_pico_usd, u.actual_cost_pico_usd, u.discount_pico_usd, u.discount_percent_bps
		FROM proxy_requests r LEFT JOIN request_usage u ON u.request_id = r.id
		ORDER BY r.received_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	result := make([]RequestStat, 0)
	for rows.Next() {
		var item RequestStat
		var completedAt, errorCode sql.NullString
		var duration sql.NullInt64
		var input, output, total, cachedRead, cacheWrite, reasoning, estimated sql.NullInt64
		var official, actual, discount, discountBPS sql.NullInt64
		if err := rows.Scan(&item.ID, &item.Protocol, &item.Model, &item.State, &item.ReceivedAt, &completedAt, &duration, &errorCode,
			&item.Provider, &item.UpstreamModel, &item.Attempts, &input, &output, &total, &cachedRead, &cacheWrite, &reasoning, &estimated, &official, &actual, &discount, &discountBPS); err != nil {
			return nil, err
		}
		if completedAt.Valid {
			item.CompletedAt = &completedAt.String
		}
		if errorCode.Valid {
			item.ErrorCode = &errorCode.String
		}
		if duration.Valid {
			item.DurationMS = &duration.Int64
		}
		item.InputTokens, item.OutputTokens, item.TotalTokens = input.Int64, output.Int64, total.Int64
		item.CachedReadTokens, item.CacheWriteTokens, item.ReasoningTokens = cachedRead.Int64, cacheWrite.Int64, reasoning.Int64
		item.EstimatedCostPico = estimated.Int64
		if official.Valid {
			item.OfficialCostPico = &official.Int64
		}
		if actual.Valid {
			item.ActualCostPico = &actual.Int64
		}
		if discount.Valid {
			item.DiscountPico = &discount.Int64
		}
		if discountBPS.Valid {
			item.DiscountBPS = &discountBPS.Int64
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for index := range result {
		attemptRows, err := s.db.QueryContext(ctx, `SELECT attempt_number, COALESCE(provider, ''), COALESCE(upstream_model, ''), state, started_at, COALESCE(completed_at, ''), COALESCE(error_class, ''), COALESCE(error_message, ''), COALESCE(error_raw, '') FROM proxy_attempts WHERE request_id = ? ORDER BY attempt_number`, result[index].ID)
		if err != nil {
			return nil, err
		}
		for attemptRows.Next() {
			var attempt AttemptStat
			if err := attemptRows.Scan(&attempt.Number, &attempt.Provider, &attempt.UpstreamModel, &attempt.State, &attempt.StartedAt, &attempt.CompletedAt, &attempt.ErrorClass, &attempt.ErrorMessage, &attempt.RawError); err != nil {
				attemptRows.Close()
				return nil, err
			}
			result[index].AttemptDetails = append(result[index].AttemptDetails, attempt)
		}
		if err := attemptRows.Err(); err != nil {
			attemptRows.Close()
			return nil, err
		}
		if err := attemptRows.Close(); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (s *Store) RequestStatsSummary(ctx context.Context) (StatsSummary, error) {
	var summary StatsSummary
	err := s.db.QueryRowContext(ctx, `SELECT
		COUNT(*),
		COALESCE(SUM(CASE WHEN r.state = 'succeeded' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN r.state = 'failed' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN r.state = 'partial' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(u.input_tokens), 0),
		COALESCE(SUM(u.output_tokens), 0),
		COALESCE(SUM(u.total_tokens), 0),
		COALESCE(SUM(u.cached_read_tokens), 0),
		COALESCE(SUM(u.cache_write_tokens), 0),
		COALESCE(SUM(u.reasoning_tokens), 0),
		COALESCE(SUM(u.estimated_cost_pico_usd), 0),
		COALESCE(SUM(u.actual_cost_pico_usd), 0),
		COALESCE(SUM(CASE WHEN u.discount_pico_usd > 0 THEN u.discount_pico_usd ELSE 0 END), 0),
		COUNT(u.actual_cost_pico_usd),
		COALESCE(SUM(r.attempt_count), 0),
		COALESCE(SUM(CASE WHEN r.attempt_count > 1 THEN 1 ELSE 0 END), 0),
		MIN(r.duration_ms), MAX(r.duration_ms), CAST(AVG(r.duration_ms) AS INTEGER), COUNT(r.duration_ms)
		FROM proxy_requests r LEFT JOIN request_usage u ON u.request_id = r.id`).Scan(
		&summary.TotalRequests, &summary.SucceededRequests, &summary.FailedRequests, &summary.PartialRequests,
		&summary.InputTokens, &summary.OutputTokens, &summary.TotalTokens, &summary.CachedReadTokens,
		&summary.CacheWriteTokens, &summary.ReasoningTokens, &summary.EstimatedCostPico,
		&summary.ActualCostPico, &summary.SavedCostPico, &summary.RequestsWithActual,
		&summary.TotalAttempts, &summary.RetriedRequests, &summary.FastestMS, &summary.SlowestMS, &summary.AverageMS, &summary.RequestsWithTime)
	return summary, err
}

func (s *Store) ModelStats(ctx context.Context, freeModels map[string]bool) ([]ModelStats, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT
		r.logical_model,
		CASE WHEN EXISTS (SELECT 1 FROM proxy_attempts pa JOIN proxy_requests rp ON rp.id = pa.request_id WHERE rp.logical_model = r.logical_model AND pa.upstream_model LIKE '%:free') THEN 1 ELSE 0 END,
		COUNT(*),
		COALESCE(SUM(CASE WHEN r.state = 'succeeded' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN r.state = 'failed' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN r.state = 'partial' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(r.attempt_count), 0),
		COALESCE(SUM(CASE WHEN r.attempt_count > 1 THEN 1 ELSE 0 END), 0),
		MIN(r.duration_ms), MAX(r.duration_ms), CAST(AVG(r.duration_ms) AS INTEGER), COUNT(r.duration_ms),
		COALESCE(SUM(u.input_tokens), 0), COALESCE(SUM(u.output_tokens), 0), COALESCE(SUM(u.total_tokens), 0),
		COALESCE(SUM(u.cached_read_tokens), 0), COALESCE(SUM(u.cache_write_tokens), 0), COALESCE(SUM(u.reasoning_tokens), 0),
		COALESCE(SUM(u.estimated_cost_pico_usd), 0), COALESCE(SUM(u.official_cost_pico_usd), 0), COALESCE(SUM(u.actual_cost_pico_usd), 0),
		COALESCE(SUM(u.discount_pico_usd), 0), COALESCE(SUM(CASE WHEN u.discount_pico_usd > 0 THEN u.discount_pico_usd ELSE 0 END), 0)
		FROM proxy_requests r LEFT JOIN request_usage u ON u.request_id = r.id
		GROUP BY r.logical_model ORDER BY COUNT(*) DESC, r.logical_model`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]ModelStats, 0)
	for rows.Next() {
		var item ModelStats
		var observedFree int
		var fastest, slowest, average sql.NullInt64
		if err := rows.Scan(&item.Model, &observedFree, &item.Requests, &item.SucceededRequests, &item.FailedRequests, &item.PartialRequests,
			&item.TotalAttempts, &item.RetriedRequests, &fastest, &slowest, &average, &item.RequestsWithTime,
			&item.InputTokens, &item.OutputTokens, &item.TotalTokens, &item.CachedReadTokens, &item.CacheWriteTokens, &item.ReasoningTokens,
			&item.EstimatedCostPico, &item.OfficialCostPico, &item.ActualCostPico, &item.DiscountPico, &item.SavedCostPico); err != nil {
			return nil, err
		}
		item.Free = freeModels[item.Model] || observedFree != 0
		if item.Requests > 0 {
			item.SuccessRateBPS = item.SucceededRequests * 10000 / item.Requests
			item.RetryRateBPS = item.RetriedRequests * 10000 / item.Requests
		}
		if item.OfficialCostPico > 0 {
			value := item.DiscountPico * 10000 / item.OfficialCostPico
			item.DiscountBPS = &value
		}
		if fastest.Valid {
			item.FastestMS = &fastest.Int64
		}
		if slowest.Valid {
			item.SlowestMS = &slowest.Int64
		}
		if average.Valid {
			item.AverageMS = &average.Int64
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

type migration struct {
	name     string
	checksum string
	contents string
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.Glob(migrationFS, "migrations/*.sql")
	if err != nil {
		return nil, err
	}
	sort.Strings(entries)
	result := make([]migration, 0, len(entries))
	for _, entry := range entries {
		contents, err := fs.ReadFile(migrationFS, entry)
		if err != nil {
			return nil, err
		}
		result = append(result, migration{name: strings.TrimPrefix(entry, "migrations/"), checksum: checksum(contents), contents: string(contents)})
	}
	return result, nil
}

func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		name TEXT PRIMARY KEY,
		checksum TEXT NOT NULL,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("create migration table: %w", err)
	}
	migrations, err := loadMigrations()
	if err != nil {
		return fmt.Errorf("load migrations: %w", err)
	}
	for _, migration := range migrations {
		var appliedChecksum string
		err := s.db.QueryRowContext(ctx, `SELECT checksum FROM schema_migrations WHERE name = ?`, migration.name).Scan(&appliedChecksum)
		if err == nil {
			if appliedChecksum != migration.checksum {
				return fmt.Errorf("migration checksum mismatch for %s", migration.name)
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read migration %s: %w", migration.name, err)
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", migration.name, err)
		}
		if _, err = tx.ExecContext(ctx, migration.contents); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", migration.name, err)
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO schema_migrations(name, checksum, applied_at) VALUES(?, ?, ?)`, migration.name, migration.checksum, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %s: %w", migration.name, err)
		}
		if err = tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", migration.name, err)
		}
	}
	return nil
}

func checksum(contents []byte) string {
	hash := sha256.Sum256(contents)
	return hex.EncodeToString(hash[:])
}
