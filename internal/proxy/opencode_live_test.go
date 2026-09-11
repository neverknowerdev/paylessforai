package proxy

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/neverknowerdev/paylessforai/internal/matcher"
	"github.com/neverknowerdev/paylessforai/internal/providers"
	"github.com/neverknowerdev/paylessforai/internal/wire"
)

// Opt-in live check using synthetic history in the request shape captured from
// OpenCode 1.14.39. No local system prompt or user workspace data is transmitted.
func TestOpenCodeGoLiveRequest(t *testing.T) {
	key := os.Getenv("PLFA_TEST_OPENCODE_KEY")
	if key == "" {
		t.Skip("requires PLFA_TEST_OPENCODE_KEY")
	}
	provider := &translatingProvider{HTTPClient: providers.NewHTTPClient("opencode-go", "https://opencode.ai/zen/go/v1", key), models: []providers.Model{model("muse-spark-1.3-contributor", 1, 1)}}
	proxy, db, secret := testProxy(t, provider)
	defer db.Close()
	body := `{"model":"muse-spark-1.3-contributor","max_tokens":512,"messages":[{"role":"system","content":"You are a test assistant. Follow the user instructions."},{"role":"user","content":"Reply HELLO_VERIFIED."},{"role":"assistant","content":"HELLO_VERIFIED"},{"role":"user","content":"Reply HELLO_VERIFIED again. Do not use tools."}],"stream":true,"stream_options":{"include_usage":true}}`
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+secret)
	response := httptest.NewRecorder()
	proxy.ServeHTTP(response, request, matcher.ProtocolChatCompletions)
	t.Logf("upstream paths: %v", provider.paths)
	rows, err := db.DB().Query(`SELECT error_message FROM proxy_attempts ORDER BY attempt_number`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var msg *string
		if err := rows.Scan(&msg); err != nil {
			t.Fatal(err)
		}
		if msg != nil {
			t.Log(*msg)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK {
		t.Fatalf("response: %d %s", response.Code, response.Body.String())
	}
	events, err := wire.DecodeResponse(wire.FormatChatCompletions, response.Result())
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	for _, event := range events.Events {
		text.WriteString(event.Text)
		if event.Response != nil {
			text.WriteString(event.Response.Text)
		}
	}
	if !strings.Contains(text.String(), "HELLO_VERIFIED") {
		t.Fatalf("unexpected output: %q", text.String())
	}
}
