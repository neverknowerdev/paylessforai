package providers

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/neverknowerdev/paylessforai/internal/wire"
)

// Endpoint preserves both a configured complete endpoint and its reusable
// base. Query parameters are intentionally retained on both values.
type Endpoint struct {
	BaseURL      url.URL
	ExactURL     *url.URL
	HintedFormat wire.Format
}

func ParseEndpoint(raw string) (Endpoint, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return Endpoint{}, fmt.Errorf("endpoint URL is required")
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return Endpoint{}, fmt.Errorf("parse endpoint URL: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return Endpoint{}, fmt.Errorf("endpoint URL must include scheme and host")
	}
	if parsed.Fragment != "" {
		return Endpoint{}, fmt.Errorf("endpoint URL must not contain a fragment")
	}
	endpoint := Endpoint{BaseURL: *parsed}
	escaped := parsed.EscapedPath()
	for suffix, format := range map[string]wire.Format{"/chat/completions": wire.FormatChatCompletions, "/responses": wire.FormatResponses, "/messages": wire.FormatAnthropicMessages} {
		if strings.HasSuffix(escaped, suffix) || strings.HasSuffix(escaped, suffix+"/") {
			endpoint.HintedFormat = format
			exact := *parsed
			endpoint.ExactURL = &exact
			baseEscaped := strings.TrimSuffix(escaped, suffix+"/")
			baseEscaped = strings.TrimSuffix(baseEscaped, suffix)
			baseEscaped = strings.TrimSuffix(baseEscaped, "/")
			endpoint.BaseURL.Path, endpoint.BaseURL.RawPath = splitEscapedPath(baseEscaped)
			return endpoint, nil
		}
	}
	return endpoint, nil
}

func splitEscapedPath(escaped string) (string, string) {
	decoded, err := url.PathUnescape(escaped)
	if err != nil {
		return escaped, ""
	}
	if decoded == "" {
		return "", ""
	}
	if decoded == escaped {
		return decoded, ""
	}
	return decoded, escaped
}

func URLForFormat(endpoint Endpoint, format wire.Format) (url.URL, error) {
	if err := format.Validate(); err != nil {
		return url.URL{}, err
	}
	if endpoint.ExactURL != nil && endpoint.HintedFormat == format {
		return *endpoint.ExactURL, nil
	}
	result := endpoint.BaseURL
	suffix := map[wire.Format]string{wire.FormatChatCompletions: "/chat/completions", wire.FormatResponses: "/responses", wire.FormatAnthropicMessages: "/messages"}[format]
	path := result.EscapedPath()
	path = strings.TrimSuffix(path, "/")
	path += suffix
	result.Path, result.RawPath = splitEscapedPath(path)
	return result, nil
}

// CandidateFormats returns a stable, deduplicated search order. Unknown
// saved/hinted values are ignored instead of becoming network requests.
func CandidateFormats(saved, hinted, client wire.Format) []wire.Format {
	result := make([]wire.Format, 0, 3)
	for _, candidate := range []wire.Format{saved, hinted, client, wire.FormatChatCompletions, wire.FormatResponses, wire.FormatAnthropicMessages} {
		if !candidate.Valid() {
			continue
		}
		seen := false
		for _, existing := range result {
			if existing == candidate {
				seen = true
				break
			}
		}
		if !seen {
			result = append(result, candidate)
		}
	}
	return result
}
