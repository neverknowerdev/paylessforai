package providers

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/neverknowerdev/paylessforai/internal/buildinfo"
)

type openCodeRequestHeaders struct{}

// Custom credentials may use a display name instead of a built-in provider ID.
// Recognize the API host as well so existing credentials need no migration.
func isOpenCode(provider, baseURL string) bool {
	name := strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(provider))), "-")
	if name == "opencode" || name == "opencode-go" || name == "opencode-zen" {
		return true
	}
	endpoint, err := url.Parse(baseURL)
	return err == nil && strings.EqualFold(endpoint.Hostname(), "opencode.ai")
}

func (openCodeRequestHeaders) Apply(headers http.Header, sessionID string) {
	if sessionID != "" {
		headers.Set("x-opencode-session", sessionID)
	}
	headers.Set("User-Agent", "PayLessForAI/"+buildinfo.Version)
}
