package session

import (
	"context"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/neverknowerdev/paylessforai/internal/wire"
)

type fakeStore struct {
	upserts       int
	resolves      int
	selected      string
	upsertedValue string
}

func (s *fakeStore) UpsertSession(_ context.Context, _, sessionID string, _ time.Time) error {
	s.upserts++
	s.upsertedValue = sessionID
	return nil
}

func (s *fakeStore) ResolveAndRegister(_ context.Context, _ string, _ []string, _, _, fresh string, _ time.Time) (string, error) {
	s.resolves++
	if s.selected != "" {
		return s.selected, nil
	}
	return fresh, nil
}

func testDetector(store Store) *Detector {
	detector := NewDetector(store, []byte("test-installation-key"))
	detector.now = func() time.Time { return time.Unix(100, 0) }
	detector.newID = func() string { return "generated-id" }
	return detector
}

func TestSelectedHeaderUsesPrecedenceAndBypassesHistory(t *testing.T) {
	store := &fakeStore{}
	detector := testDetector(store)
	body := []byte(`{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.invalid/image"}}]}]}`)
	got := detector.DetectSessionID(context.Background(), Input{
		ClientKeyID: "client-a",
		Format:      wire.FormatChatCompletions,
		Headers: http.Header{
			"x-opencode-session": []string{"fallback"},
			"X-PayLess-Chat-Id":  []string{"  exact-value  "},
		},
		Body: body,
	})
	if got != "exact-value" || store.upserts != 1 || store.resolves != 0 || store.upsertedValue != got {
		t.Fatalf("header detection: got %q, store=%+v", got, store)
	}
}

func TestHeadersFallThroughInvalidAndDuplicateValues(t *testing.T) {
	store := &fakeStore{}
	detector := testDetector(store)
	for name, headers := range map[string]http.Header{
		"duplicate": {"X-PayLess-Chat-Id": {"one", "two"}, "x-opencode-session": {"valid"}},
		"control":   {"X-PayLess-Chat-Id": {"bad\nvalue"}, "x-opencode-session": {"valid"}},
		"oversized": {"X-PayLess-Chat-Id": {string(make([]byte, 257))}, "x-opencode-session": {"valid"}},
	} {
		if got := detector.DetectSessionID(context.Background(), Input{ClientKeyID: "client", Headers: headers}); got != "valid" {
			t.Errorf("%s: got %q", name, got)
		}
	}
	if store.resolves != 0 || store.upserts != 3 {
		t.Fatalf("invalid headers should still use the supported fallback: %+v", store)
	}
}

func TestHeaderlessPrefixesReuseAndPreserveOriginalBody(t *testing.T) {
	store := &fakeStore{selected: "existing"}
	detector := testDetector(store)
	body := []byte(`{"model":"model-a","messages":[{"role":"system","content":"ignored"},{"role":"user","content":"  first\r\n"},{"role":"assistant","content":"different"},{"role":"user","content":"second"}]}`)
	original := append([]byte(nil), body...)
	got := detector.DetectSessionID(context.Background(), Input{ClientKeyID: "client", Format: wire.FormatChatCompletions, Body: body})
	if got != "existing" || store.resolves != 1 || !reflect.DeepEqual(body, original) {
		t.Fatalf("headerless detection: got %q, resolves=%d, body changed=%q", got, store.resolves, body)
	}
}

func TestHistoryProjectionExcludesToolsAndAssistant(t *testing.T) {
	withTool := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"first"}]},{"role":"assistant","content":"answer"},{"role":"user","content":[{"type":"tool_result","tool_use_id":"x","content":"changing"},{"type":"text","text":"second"}]}]}`)
	withoutTool := []byte(`{"messages":[{"role":"user","content":"first"},{"role":"user","content":"second"}]}`)
	a, ok := extractHistory(wire.FormatAnthropicMessages, withTool)
	if !ok {
		t.Fatal("tool projection rejected")
	}
	b, ok := extractHistory(wire.FormatChatCompletions, withoutTool)
	if !ok || !reflect.DeepEqual(a, b) {
		t.Fatalf("history projection differs: %#v vs %#v", a, b)
	}
	if _, ok := extractHistory(wire.FormatChatCompletions, []byte(`{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.invalid"}}]}]}`)); ok {
		t.Fatal("unsupported remote media must skip history detection")
	}
	inlineA, ok := extractHistory(wire.FormatChatCompletions, []byte(`{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]}]}`))
	if !ok || len(inlineA) != 1 {
		t.Fatalf("inline data media should be supported: %#v, %v", inlineA, ok)
	}
	inlineB, ok := extractHistory(wire.FormatChatCompletions, []byte(`{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,BBBB"}}]}]}`))
	if !ok || len(inlineB) != 1 || inlineA[0].value == inlineB[0].value {
		t.Fatalf("inline media should affect the fingerprint: %#v, %#v, %v", inlineA, inlineB, ok)
	}
}

func TestResponsesStringAndHashBoundaries(t *testing.T) {
	items, ok := extractHistory(wire.FormatResponses, []byte(`{"input":"hello"}`))
	if !ok || len(items) != 1 || items[0].value != "hello" {
		t.Fatalf("responses projection: %#v, %v", items, ok)
	}
	first := prefixHashes([]byte("key"), "client", []historyItem{{kind: "text", value: "ab"}, {kind: "text", value: "c"}})
	second := prefixHashes([]byte("key"), "client", []historyItem{{kind: "text", value: "a"}, {kind: "text", value: "bc"}})
	if reflect.DeepEqual(first, second) {
		t.Fatal("length-delimited prefixes must distinguish boundaries")
	}
}

func TestDetectorFailsOpen(t *testing.T) {
	store := &failingStore{}
	detector := testDetector(store)
	if got := detector.DetectSessionID(context.Background(), Input{ClientKeyID: "client", Format: wire.FormatResponses, Body: []byte(`{"input":"hello"}`)}); got != "generated-id" {
		t.Fatalf("storage failure should return fresh ID, got %q", got)
	}
	if got := detector.DetectSessionID(context.Background(), Input{ClientKeyID: "client", Headers: http.Header{"X-PayLess-Chat-Id": {"trusted"}}}); got != "trusted" {
		t.Fatalf("header must survive storage failure, got %q", got)
	}
}

type failingStore struct{}

func (failingStore) UpsertSession(context.Context, string, string, time.Time) error {
	return context.DeadlineExceeded
}
func (failingStore) ResolveAndRegister(context.Context, string, []string, string, string, string, time.Time) (string, error) {
	return "", context.DeadlineExceeded
}
