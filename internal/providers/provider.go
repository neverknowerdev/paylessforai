package providers

import (
	"context"
	"net/http"

	"github.com/neverknowerdev/paylessforai/internal/matcher"
	"github.com/neverknowerdev/paylessforai/internal/retry"
)

type Model struct {
	ID                     string
	Name                   string
	Free                   bool
	ContextLength          int64
	MaxCompletionTokens    int64
	Pricing                matcher.Price
	PriceAvailable         bool
	OfficialPricing        matcher.Price
	OfficialPriceAvailable bool
	SupportedParameters    []string
	InputModalities        []string
	OutputModalities       []string
	Tags                   []string
}

// ManualModel is a model definition supplied by the user when an upstream
// does not expose a catalog endpoint. Prices are pico-USD per token so the
// manually configured route uses the same deterministic accounting as native
// provider metadata.
type ManualModel struct {
	ID                    string   `json:"id"`
	InputPicoUSDPerToken  int64    `json:"input_price_pico_usd_per_token"`
	OutputPicoUSDPerToken int64    `json:"output_price_pico_usd_per_token"`
	ContextLength         int64    `json:"context_length,omitempty"`
	MaxCompletionTokens   int64    `json:"max_output_tokens,omitempty"`
	InputModalities       []string `json:"input_modalities,omitempty"`
	OutputModalities      []string `json:"output_modalities,omitempty"`
	Tags                  []string `json:"tags,omitempty"`
}

// ModelVerifier is implemented by clients that can validate explicit model
// IDs with a minimal, non-streaming inference request.
type ModelVerifier interface {
	VerifyModels(context.Context, []ManualModel) ([]Model, error)
}

type Client interface {
	Name() string
	Discover(context.Context) ([]Model, error)
	Do(context.Context, matcher.Protocol, string, []byte) (*http.Response, error)
}

type UpstreamError struct {
	Provider   string
	StatusCode int
	Class      retry.ErrorClass
	Message    string
	RetryAfter *int
}

func (e *UpstreamError) Error() string { return e.Provider + " upstream error: " + e.Message }
