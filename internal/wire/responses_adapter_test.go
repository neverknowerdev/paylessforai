package wire

import (
	"encoding/json"
	"testing"
)

func TestResponsesHistoryUsesRoleSpecificTextTypes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		format Format
		body   string
	}{
		{"chat strings", FormatChatCompletions, `{"model":"m","messages":[{"role":"system","content":"rules"},{"role":"developer","content":"more rules"},{"role":"user","content":"hello"},{"role":"assistant","content":"hello back"},{"role":"user","content":"continue"}]}`},
		{"chat blocks", FormatChatCompletions, `{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"hello"}]},{"role":"assistant","content":[{"type":"text","text":"hello back"}]},{"role":"user","content":"continue"}]}`},
		{"anthropic", FormatAnthropicMessages, `{"model":"m","max_tokens":32,"messages":[{"role":"user","content":"hello"},{"role":"assistant","content":[{"type":"text","text":"hello back"}]},{"role":"user","content":"continue"}]}`},
		{"responses", FormatResponses, `{"model":"m","input":[{"role":"user","content":[{"type":"input_text","text":"hello"}]},{"role":"assistant","content":[{"type":"output_text","text":"hello back"}]},{"role":"user","content":"continue"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := DecodeRequest(tc.format, []byte(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := EncodeRequest(FormatResponses, req)
			if err != nil {
				t.Fatal(err)
			}
			var payload struct {
				Input []struct {
					Role    string
					Content []struct {
						Type string
						Text string
					}
				}
			}
			if err := json.Unmarshal(encoded, &payload); err != nil {
				t.Fatal(err)
			}
			if len(payload.Input) != len(req.Messages) {
				t.Fatalf("lost history: %s", encoded)
			}
			for i, msg := range payload.Input {
				want := "input_text"
				if msg.Role == "assistant" {
					want = "output_text"
				}
				if len(msg.Content) != 1 || msg.Content[0].Type != want || msg.Content[0].Text != req.Messages[i].Content[0].Text {
					t.Fatalf("invalid history at %d: %s", i, encoded)
				}
			}
		})
	}
}
