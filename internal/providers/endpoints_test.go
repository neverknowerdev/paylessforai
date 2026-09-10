package providers

import (
	"net/url"
	"strings"
	"testing"

	"github.com/neverknowerdev/paylessforai/internal/wire"
)

func TestEndpointParsingAndCandidateOrder(t *testing.T) {
	endpoint, err := ParseEndpoint("https://example.test/prefix%2Fv1/responses/?tenant=one")
	if err != nil {
		t.Fatal(err)
	}
	if endpoint.HintedFormat != wire.FormatResponses || endpoint.ExactURL == nil {
		t.Fatalf("unexpected hint: %#v", endpoint)
	}
	if got := endpoint.BaseURL.EscapedPath(); got != "/prefix%2Fv1" {
		t.Fatalf("base path %q", got)
	}
	if got := endpoint.ExactURL.EscapedPath(); got != "/prefix%2Fv1/responses/" {
		t.Fatalf("exact path %q", got)
	}
	urlValue, err := URLForFormat(endpoint, wire.FormatChatCompletions)
	if err != nil {
		t.Fatal(err)
	}
	if urlValue.EscapedPath() != "/prefix%2Fv1/chat/completions" || urlValue.RawQuery != "tenant=one" {
		t.Fatalf("alternative URL %s", urlValue.String())
	}
	urlValue = URLForFormatMust(endpoint, wire.FormatResponses)
	if got := urlValue.EscapedPath(); got != "/prefix%2Fv1/responses/" {
		t.Fatalf("exact URL was not preserved: %q", got)
	}
	got := CandidateFormats(wire.FormatUnknown, wire.FormatResponses, wire.FormatChatCompletions)
	if strings.Join([]string{string(got[0]), string(got[1]), string(got[2])}, ",") != "openai_responses,openai_chat_completions,anthropic_messages" {
		t.Fatalf("candidate order %v", got)
	}
}

func URLForFormatMust(endpoint Endpoint, format wire.Format) (resultURL url.URL) {
	resultURL, _ = URLForFormat(endpoint, format)
	return resultURL
}
