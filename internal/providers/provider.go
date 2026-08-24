package providers

import (
	"context"
	"net/http"

	"github.com/neverknowerdev/paylessforai/internal/matcher"
	"github.com/neverknowerdev/paylessforai/internal/retry"
)

type Model struct {
	ID                  string
	Name                string
	Free                bool
	ContextLength       int64
	MaxCompletionTokens int64
	Pricing             matcher.Price
	PriceAvailable      bool
	SupportedParameters []string
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
