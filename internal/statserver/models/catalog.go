package models

import "time"

type CatalogRecord struct {
	SourceID, Name, Creator, Family, Revision, Description string
	Context                                                int64
	ProviderModel                                          string
	Input, Output, CacheRead, CacheWrite                   *float64
	Benchmarks                                             map[string]float64
	Metadata                                               map[string]any
}

type Source struct {
	Key           string     `json:"key"`
	DisplayName   string     `json:"display_name"`
	BaseURL       string     `json:"-"`
	LastAttemptAt *time.Time `json:"last_attempt_at"`
	LastSuccessAt *time.Time `json:"last_success_at"`
	LastError     string     `json:"last_error"`
	RecordCount   int        `json:"record_count"`
}

type ModelSummary struct {
	ID             int64     `json:"id"`
	CanonicalSlug  string    `json:"canonical_slug"`
	DisplayName    string    `json:"display_name"`
	Creator        string    `json:"creator"`
	Family         string    `json:"family"`
	Revision       string    `json:"revision"`
	Description    string    `json:"description"`
	ContextLength  *int64    `json:"context_length"`
	OfferingCount  int       `json:"offering_count"`
	BenchmarkCount int       `json:"benchmark_count"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type SearchResult struct {
	ModelSummary
	MatchType string `json:"match_type"`
}

type ModelDetail struct {
	ModelSummary
	Offerings  []Offering        `json:"offerings"`
	Benchmarks []BenchmarkResult `json:"benchmarks"`
}

type Offering struct {
	Provider                string    `json:"provider"`
	ProviderModelID         string    `json:"provider_model_id"`
	Variant                 string    `json:"variant"`
	Status                  string    `json:"status"`
	InputUSDPerMillion      *float64  `json:"input_usd_per_million"`
	OutputUSDPerMillion     *float64  `json:"output_usd_per_million"`
	CacheReadUSDPerMillion  *float64  `json:"cache_read_usd_per_million"`
	CacheWriteUSDPerMillion *float64  `json:"cache_write_usd_per_million"`
	ObservedAt              time.Time `json:"observed_at"`
}

type BenchmarkResult struct {
	Name       string    `json:"name"`
	Version    string    `json:"version"`
	Metric     string    `json:"metric"`
	Value      float64   `json:"value"`
	Unit       string    `json:"unit"`
	Verified   bool      `json:"verified"`
	Source     string    `json:"source"`
	ObservedAt time.Time `json:"observed_at"`
}
