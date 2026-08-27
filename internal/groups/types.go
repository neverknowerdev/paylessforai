package groups

import "time"

type BillingClass string

const (
	BillingFree         BillingClass = "free"
	BillingSubscription BillingClass = "subscription"
	BillingMetered      BillingClass = "metered"
)

var AllBillingClasses = []BillingClass{BillingFree, BillingSubscription, BillingMetered}

type SourceKind string

const (
	SourceModel SourceKind = "model"
	SourceGroup SourceKind = "group"
)

type Source struct {
	Kind                        SourceKind `json:"kind"`
	ModelID                     string     `json:"model_id,omitempty"`
	GroupID                     string     `json:"group_id,omitempty"`
	ProviderName                string     `json:"provider_name,omitempty"`
	Retries                     *int       `json:"retries,omitempty"`
	MaximumOfficialPricePercent *int       `json:"maximum_official_price_percent,omitempty"`
}

type PriceLimits struct {
	MaximumInputPicoUSDPerToken  *int64 `json:"maximum_input_pico_usd_per_token,omitempty"`
	MaximumOutputPicoUSDPerToken *int64 `json:"maximum_output_pico_usd_per_token,omitempty"`
	MaximumExpectedCostPicoUSD   *int64 `json:"maximum_expected_cost_pico_usd,omitempty"`
}

type Stage struct {
	ID                           string         `json:"id,omitempty"`
	Name                         string         `json:"name"`
	Position                     int            `json:"position"`
	Sources                      []Source       `json:"sources"`
	ProviderNames                []string       `json:"provider_names,omitempty"`
	BillingClasses               []BillingClass `json:"billing_classes,omitempty"`
	Selection                    string         `json:"selection"`
	MaximumInputPicoUSDPerToken  *int64         `json:"maximum_input_pico_usd_per_token,omitempty"`
	MaximumOutputPicoUSDPerToken *int64         `json:"maximum_output_pico_usd_per_token,omitempty"`
	MaximumExpectedCostPicoUSD   *int64         `json:"maximum_expected_cost_pico_usd,omitempty"`
	SameRouteRetries             *int           `json:"same_route_retries,omitempty"`
	TryRetries                   *int           `json:"try_retries,omitempty"`
}

type Definition struct {
	ID          string    `json:"id,omitempty"`
	Name        string    `json:"name"`
	Slug        string    `json:"slug"`
	Description string    `json:"description,omitempty"`
	Enabled     bool      `json:"enabled"`
	Revision    int64     `json:"revision"`
	CreatedAt   time.Time `json:"created_at,omitempty"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
	Stages      []Stage   `json:"stages"`
}

type ValidationIssue struct {
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Level   string `json:"level"` // error or warning
}

func (i ValidationIssue) Error() string { return i.Code + ": " + i.Message }

type EffectiveStage struct {
	Path                         []string       `json:"path"`
	TryKey                       string         `json:"try_key"`
	Name                         string         `json:"name"`
	LogicalModelIDs              []string       `json:"logical_model_ids"`
	ProviderNames                []string       `json:"provider_names,omitempty"`
	BillingClasses               []BillingClass `json:"billing_classes"`
	Selection                    string         `json:"selection"`
	MaximumInputPicoUSDPerToken  *int64         `json:"maximum_input_pico_usd_per_token,omitempty"`
	MaximumOutputPicoUSDPerToken *int64         `json:"maximum_output_pico_usd_per_token,omitempty"`
	MaximumExpectedCostPicoUSD   *int64         `json:"maximum_expected_cost_pico_usd,omitempty"`
	SameRouteRetries             int            `json:"same_route_retries"`
	TryRetries                   int            `json:"try_retries"`
	MaximumOfficialPricePercent  *int           `json:"maximum_official_price_percent,omitempty"`
}

type CompileResult struct {
	Stages []EffectiveStage
	Issues []ValidationIssue
}
