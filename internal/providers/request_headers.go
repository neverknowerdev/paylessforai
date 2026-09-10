package providers

import "net/http"

// RequestHeaders applies provider-specific metadata without allowing session
// detection to leak into provider implementations.
type RequestHeaders interface {
	Apply(http.Header, string)
}

type noopRequestHeaders struct{}

func (noopRequestHeaders) Apply(http.Header, string) {}
