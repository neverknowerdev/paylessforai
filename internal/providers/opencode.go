package providers

import (
	"net/http"

	"github.com/neverknowerdev/paylessforai/internal/buildinfo"
)

type openCodeRequestHeaders struct{}

func (openCodeRequestHeaders) Apply(headers http.Header, sessionID string) {
	if sessionID != "" {
		headers.Set("x-opencode-session", sessionID)
	}
	headers.Set("User-Agent", "PayLessForAI/"+buildinfo.Version)
}
