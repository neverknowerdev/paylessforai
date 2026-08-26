package store

import (
	"context"
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

	"github.com/neverknowerdev/paylessforai/internal/subscription"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type Store struct {
	db           *sql.DB
	Repositories *Repositories
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
	DurationMS    *int64 `json:"duration_ms,omitempty"`
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
	TotalRequests         int64  `json:"total_requests"`
	SucceededRequests     int64  `json:"succeeded_requests"`
	FailedRequests        int64  `json:"failed_requests"`
	PartialRequests       int64  `json:"partial_requests"`
	InputTokens           int64  `json:"input_tokens"`
	OutputTokens          int64  `json:"output_tokens"`
	TotalTokens           int64  `json:"total_tokens"`
	CachedReadTokens      int64  `json:"cached_read_tokens"`
	CacheWriteTokens      int64  `json:"cache_write_tokens"`
	ReasoningTokens       int64  `json:"reasoning_tokens"`
	EstimatedCostPico     int64  `json:"estimated_cost_pico_usd"`
	OfficialCostPico      int64  `json:"official_cost_pico_usd"`
	ActualCostPico        int64  `json:"actual_cost_pico_usd"`
	SavedCostPico         int64  `json:"saved_cost_pico_usd"`
	SavedPercentBPS       *int64 `json:"saved_percent_bps,omitempty"`
	RequestsWithActual    int64  `json:"requests_with_actual_cost"`
	TotalAttempts         int64  `json:"total_attempts"`
	RetriedRequests       int64  `json:"retried_requests"`
	FastestMS             *int64 `json:"fastest_response_ms,omitempty"`
	SlowestMS             *int64 `json:"slowest_response_ms,omitempty"`
	AverageMS             *int64 `json:"average_response_ms,omitempty"`
	RequestsWithTime      int64  `json:"requests_with_response_time"`
	EligibleRequests      int64  `json:"eligible_requests"`
	ExcludedLimitRequests int64  `json:"excluded_limit_requests"`
	SuccessRateBPS        int64  `json:"success_rate_bps"`
}

type ModelStats struct {
	Model                 string `json:"model"`
	Free                  bool   `json:"free"`
	Requests              int64  `json:"requests"`
	EligibleRequests      int64  `json:"eligible_requests"`
	ExcludedLimitRequests int64  `json:"excluded_limit_requests"`
	SucceededRequests     int64  `json:"succeeded_requests"`
	FailedRequests        int64  `json:"failed_requests"`
	PartialRequests       int64  `json:"partial_requests"`
	SuccessRateBPS        int64  `json:"success_rate_bps"`
	TotalAttempts         int64  `json:"total_attempts"`
	RetriedRequests       int64  `json:"retried_requests"`
	RetryRateBPS          int64  `json:"retry_rate_bps"`
	FastestMS             *int64 `json:"fastest_response_ms,omitempty"`
	SlowestMS             *int64 `json:"slowest_response_ms,omitempty"`
	AverageMS             *int64 `json:"average_response_ms,omitempty"`
	RequestsWithTime      int64  `json:"requests_with_response_time"`
	InputTokens           int64  `json:"input_tokens"`
	OutputTokens          int64  `json:"output_tokens"`
	TotalTokens           int64  `json:"total_tokens"`
	CachedReadTokens      int64  `json:"cached_read_tokens"`
	CacheWriteTokens      int64  `json:"cache_write_tokens"`
	ReasoningTokens       int64  `json:"reasoning_tokens"`
	EstimatedCostPico     int64  `json:"estimated_cost_pico_usd"`
	OfficialCostPico      int64  `json:"official_cost_pico_usd"`
	ActualCostPico        int64  `json:"actual_cost_pico_usd"`
	SavedCostPico         int64  `json:"saved_cost_pico_usd"`
	DiscountPico          int64  `json:"discount_pico_usd"`
	DiscountBPS           *int64 `json:"discount_percent_bps,omitempty"`
}

// ProviderStats aggregates terminal request outcomes by the provider that
// ultimately served each request. Retry attempts are included in the attempt
// totals while spend and usage remain attached to the completed request.
type ProviderStats struct {
	Provider              string `json:"provider"`
	Requests              int64  `json:"requests"`
	EligibleRequests      int64  `json:"eligible_requests"`
	ExcludedLimitRequests int64  `json:"excluded_limit_requests"`
	SucceededRequests     int64  `json:"succeeded_requests"`
	FailedRequests        int64  `json:"failed_requests"`
	PartialRequests       int64  `json:"partial_requests"`
	SuccessRateBPS        int64  `json:"success_rate_bps"`
	TotalAttempts         int64  `json:"total_attempts"`
	RetriedRequests       int64  `json:"retried_requests"`
	RetryRateBPS          int64  `json:"retry_rate_bps"`
	FastestMS             *int64 `json:"fastest_response_ms,omitempty"`
	SlowestMS             *int64 `json:"slowest_response_ms,omitempty"`
	AverageMS             *int64 `json:"average_response_ms,omitempty"`
	RequestsWithTime      int64  `json:"requests_with_response_time"`
	InputTokens           int64  `json:"input_tokens"`
	OutputTokens          int64  `json:"output_tokens"`
	TotalTokens           int64  `json:"total_tokens"`
	CachedReadTokens      int64  `json:"cached_read_tokens"`
	CacheWriteTokens      int64  `json:"cache_write_tokens"`
	ReasoningTokens       int64  `json:"reasoning_tokens"`
	EstimatedCostPico     int64  `json:"estimated_cost_pico_usd"`
	OfficialCostPico      int64  `json:"official_cost_pico_usd"`
	ActualCostPico        int64  `json:"actual_cost_pico_usd"`
	SavedCostPico         int64  `json:"saved_cost_pico_usd"`
	DiscountBPS           *int64 `json:"discount_percent_bps,omitempty"`
}

type ProviderCredential struct {
	ID                     string  `json:"id"`
	Provider               string  `json:"provider"`
	Label                  string  `json:"label"`
	BaseURL                string  `json:"base_url,omitempty"`
	Ciphertext             []byte  `json:"-"`
	Nonce                  []byte  `json:"-"`
	Enabled                bool    `json:"enabled"`
	CreatedAt              string  `json:"created_at"`
	UpdatedAt              string  `json:"updated_at"`
	LastCheckedAt          *string `json:"last_checked_at,omitempty"`
	LastError              *string `json:"last_error,omitempty"`
	ManualModelsJSON       string  `json:"-"`
	AccessMode             string  `json:"access_mode"`
	SubscriptionFeePicoUSD *int64  `json:"subscription_fee_pico_usd,omitempty"`
	SubscriptionCycleStart *string `json:"subscription_cycle_start,omitempty"`
	SubscriptionCycleEnd   *string `json:"subscription_cycle_end,omitempty"`
	SubscriptionStatus     string  `json:"subscription_status"`
	NextAvailableAt        *string `json:"next_available_at,omitempty"`
	StatusReason           *string `json:"status_reason,omitempty"`
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
	store := &Store{db: db, Repositories: newRepositories(db)}
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
	return s.Repositories.ClientAPIKeys.Create(ctx, label)
}

func (s *Store) AuthenticateClientKey(ctx context.Context, secret string) (ClientKey, bool, error) {
	return s.Repositories.ClientAPIKeys.Authenticate(ctx, secret)
}

func (s *Store) ListClientKeys(ctx context.Context) ([]ClientKey, error) {
	return s.Repositories.ClientAPIKeys.List(ctx)
}

func (s *Store) RevokeClientKey(ctx context.Context, id string) error {
	return s.Repositories.ClientAPIKeys.Revoke(ctx, id)
}

func (s *Store) UpsertProviderCredential(ctx context.Context, credential ProviderCredential) error {
	return s.Repositories.ProviderCredentials.Upsert(ctx, credential)
}

func (s *Store) ListProviderCredentials(ctx context.Context) ([]ProviderCredential, error) {
	return s.Repositories.ProviderCredentials.List(ctx)
}

// MarkProviderLimited persists a provider/account limit and its best-known reset time.
func (s *Store) MarkProviderLimited(ctx context.Context, provider string, next *time.Time, reason string) error {
	return s.Repositories.ProviderCredentials.MarkLimited(ctx, provider, next, reason)
}

func NULLString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func (s *Store) ClearExpiredProviderLimits(ctx context.Context, now time.Time) error {
	return s.Repositories.ProviderCredentials.ClearExpired(ctx, now)
}

func (s *Store) SubscriptionUsage(ctx context.Context) ([]subscription.UsageRow, error) {
	return s.Repositories.ProviderCredentials.Usage(ctx)
}

func (s *Store) SubscriptionPricing(ctx context.Context) ([]subscription.Pricing, error) {
	rows, err := s.SubscriptionUsage(ctx)
	if err != nil {
		return nil, err
	}
	return subscription.Calculate(rows), nil
}

func (s *Store) DeleteProviderCredential(ctx context.Context, id string) error {
	return s.Repositories.ProviderCredentials.Delete(ctx, id)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (s *Store) CreateProxyRequest(ctx context.Context, id, clientKeyID, protocol, model string) error {
	return s.Repositories.ProxyRequests.Create(ctx, id, clientKeyID, protocol, model)
}

func (s *Store) CompleteProxyRequest(ctx context.Context, id, state, errorCode, errorMessage string) error {
	return s.Repositories.ProxyRequests.Complete(ctx, id, state, errorCode, errorMessage)
}

// RecordProxyAttempt records both the route actually contacted and the durable
// attempt count used by the request UI and postmortem diagnostics.
func (s *Store) RecordProxyAttempt(ctx context.Context, requestID string, attempt int, provider, upstream, state, errorClass, errorMessage string, rawError ...string) error {
	return s.recordProxyAttempt(ctx, requestID, attempt, provider, upstream, state, errorClass, errorMessage, rawError...)
}

func (s *Store) recordProxyAttempt(ctx context.Context, requestID string, attempt int, provider, upstream, state, errorClass, errorMessage string, rawError ...string) error {
	return s.Repositories.ProxyAttempts.Record(ctx, requestID, attempt, provider, upstream, state, errorClass, errorMessage, rawError...)
}

func (s *Store) RecordUsage(ctx context.Context, usage RequestUsage) error {
	return s.Repositories.RequestUsage.Upsert(ctx, usage)
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
		attemptRows, err := s.db.QueryContext(ctx, `SELECT attempt_number, COALESCE(provider, ''), COALESCE(upstream_model, ''), state, started_at, COALESCE(completed_at, ''), duration_ms, COALESCE(error_class, ''), COALESCE(error_message, ''), COALESCE(error_raw, '') FROM proxy_attempts WHERE request_id = ? ORDER BY attempt_number`, result[index].ID)
		if err != nil {
			return nil, err
		}
		for attemptRows.Next() {
			var attempt AttemptStat
			var duration sql.NullInt64
			if err := attemptRows.Scan(&attempt.Number, &attempt.Provider, &attempt.UpstreamModel, &attempt.State, &attempt.StartedAt, &attempt.CompletedAt, &duration, &attempt.ErrorClass, &attempt.ErrorMessage, &attempt.RawError); err != nil {
				attemptRows.Close()
				return nil, err
			}
			if duration.Valid {
				attempt.DurationMS = &duration.Int64
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
		COALESCE(SUM(u.official_cost_pico_usd), 0),
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
		&summary.OfficialCostPico, &summary.ActualCostPico, &summary.SavedCostPico, &summary.RequestsWithActual,
		&summary.TotalAttempts, &summary.RetriedRequests, &summary.FastestMS, &summary.SlowestMS, &summary.AverageMS, &summary.RequestsWithTime)
	if err == nil && summary.OfficialCostPico > 0 {
		value := summary.SavedCostPico * 10000 / summary.OfficialCostPico
		summary.SavedPercentBPS = &value
	}
	if err == nil {
		_ = s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE WHEN stats_disposition='included' THEN 1 ELSE 0 END),0), COALESCE(SUM(CASE WHEN stats_disposition='excluded_limit' THEN 1 ELSE 0 END),0) FROM proxy_requests`).Scan(&summary.EligibleRequests, &summary.ExcludedLimitRequests)
		if summary.EligibleRequests > 0 {
			summary.SuccessRateBPS = summary.SucceededRequests * 10000 / summary.EligibleRequests
		}
	}
	return summary, err
}

func (s *Store) ModelStats(ctx context.Context, freeModels map[string]bool) ([]ModelStats, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT
		r.logical_model,
		CASE WHEN EXISTS (SELECT 1 FROM proxy_attempts pa JOIN proxy_requests rp ON rp.id = pa.request_id WHERE rp.logical_model = r.logical_model AND pa.upstream_model LIKE '%:free') THEN 1 ELSE 0 END,
		COUNT(*),
		COALESCE(SUM(CASE WHEN r.stats_disposition='included' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN r.stats_disposition='excluded_limit' THEN 1 ELSE 0 END),0),
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
		if err := rows.Scan(&item.Model, &observedFree, &item.Requests, &item.EligibleRequests, &item.ExcludedLimitRequests, &item.SucceededRequests, &item.FailedRequests, &item.PartialRequests,
			&item.TotalAttempts, &item.RetriedRequests, &fastest, &slowest, &average, &item.RequestsWithTime,
			&item.InputTokens, &item.OutputTokens, &item.TotalTokens, &item.CachedReadTokens, &item.CacheWriteTokens, &item.ReasoningTokens,
			&item.EstimatedCostPico, &item.OfficialCostPico, &item.ActualCostPico, &item.DiscountPico, &item.SavedCostPico); err != nil {
			return nil, err
		}
		item.Free = freeModels[item.Model] || observedFree != 0
		if item.Requests > 0 {
			if item.EligibleRequests > 0 {
				item.SuccessRateBPS = item.SucceededRequests * 10000 / item.EligibleRequests
			}
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

func (s *Store) ProviderStats(ctx context.Context) ([]ProviderStats, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT
		COALESCE(NULLIF(r.selected_provider, ''), 'unknown'),
		COUNT(*),
		COALESCE(SUM(CASE WHEN r.stats_disposition='included' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN r.stats_disposition='excluded_limit' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN r.state = 'succeeded' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN r.state = 'failed' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN r.state = 'partial' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(r.attempt_count), 0),
		COALESCE(SUM(CASE WHEN r.attempt_count > 1 THEN 1 ELSE 0 END), 0),
		MIN(r.duration_ms), MAX(r.duration_ms), CAST(AVG(r.duration_ms) AS INTEGER), COUNT(r.duration_ms),
		COALESCE(SUM(u.input_tokens), 0), COALESCE(SUM(u.output_tokens), 0), COALESCE(SUM(u.total_tokens), 0),
		COALESCE(SUM(u.cached_read_tokens), 0), COALESCE(SUM(u.cache_write_tokens), 0), COALESCE(SUM(u.reasoning_tokens), 0),
		COALESCE(SUM(u.estimated_cost_pico_usd), 0), COALESCE(SUM(u.official_cost_pico_usd), 0), COALESCE(SUM(u.actual_cost_pico_usd), 0),
		COALESCE(SUM(CASE WHEN u.discount_pico_usd > 0 THEN u.discount_pico_usd ELSE 0 END), 0)
		FROM proxy_requests r LEFT JOIN request_usage u ON u.request_id = r.id
		GROUP BY COALESCE(NULLIF(r.selected_provider, ''), 'unknown')
		ORDER BY COUNT(*) DESC, COALESCE(NULLIF(r.selected_provider, ''), 'unknown')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]ProviderStats, 0)
	for rows.Next() {
		var item ProviderStats
		var fastest, slowest, average sql.NullInt64
		if err := rows.Scan(&item.Provider, &item.Requests, &item.EligibleRequests, &item.ExcludedLimitRequests, &item.SucceededRequests, &item.FailedRequests, &item.PartialRequests,
			&item.TotalAttempts, &item.RetriedRequests, &fastest, &slowest, &average, &item.RequestsWithTime,
			&item.InputTokens, &item.OutputTokens, &item.TotalTokens, &item.CachedReadTokens, &item.CacheWriteTokens, &item.ReasoningTokens,
			&item.EstimatedCostPico, &item.OfficialCostPico, &item.ActualCostPico, &item.SavedCostPico); err != nil {
			return nil, err
		}
		if item.Requests > 0 {
			if item.EligibleRequests > 0 {
				item.SuccessRateBPS = item.SucceededRequests * 10000 / item.EligibleRequests
			}
			item.RetryRateBPS = item.RetriedRequests * 10000 / item.Requests
		}
		if item.OfficialCostPico > 0 {
			value := item.SavedCostPico * 10000 / item.OfficialCostPico
			if value < 0 {
				value = 0
			}
			if value > 10000 {
				value = 10000
			}
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
