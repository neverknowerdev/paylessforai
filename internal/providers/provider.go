package providers

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/neverknowerdev/paylessforai/internal/matcher"
	"github.com/neverknowerdev/paylessforai/internal/retry"
	"github.com/neverknowerdev/paylessforai/internal/wire"
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
	// Format is explicit upstream metadata when the provider catalog supplies
	// it. Empty means the catalog did not establish a wire format.
	Format wire.Format
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

// PreparedRequest keeps URL selection outside the HTTP transport. Legacy
// Client implementations remain supported, but translation-aware clients use
// this boundary so a caller body cannot be passed through accidentally.
type PreparedRequest struct {
	Format  wire.Format
	URL     url.URL
	Headers http.Header
	Body    io.ReadCloser
}

type Transport interface {
	Do(context.Context, PreparedRequest) (*http.Response, error)
}

type TranslationClient interface {
	Client
	Endpoint() Endpoint
	Prepare(wire.Format, string, []byte) (PreparedRequest, error)
	DoPrepared(context.Context, PreparedRequest) (*http.Response, error)
}

type UpstreamError struct {
	Provider        string
	StatusCode      int
	Class           retry.ErrorClass
	Message         string
	RetryAfter      *int
	NextAvailableAt *time.Time
}

func (e *UpstreamError) Error() string { return e.Provider + " upstream error: " + e.Message }
