// Package mockprovider provides a deterministic OpenAI/Anthropic-compatible
// upstream for integration and browser E2E tests. It never contacts an LLM.
package mockprovider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type Model struct {
	ID                  string   `json:"id"`
	Name                string   `json:"name"`
	Free                bool     `json:"free"`
	ContextLength       int64    `json:"context_length"`
	MaxCompletionTokens int64    `json:"max_completion_tokens"`
	PromptPrice         string   `json:"prompt_price"`
	CompletionPrice     string   `json:"completion_price"`
	SupportedParameters []string `json:"supported_parameters"`
	InputModalities     []string `json:"input_modalities"`
	OutputModalities    []string `json:"output_modalities"`
	SupportedFeatures   []string `json:"supported_features"`
	Tags                []string `json:"tags"`
}

type Scenario struct {
	RequireOpenCodeSession bool    `json:"require_opencode_session"`
	Models                 []Model `json:"models"`
	ResponseText           string  `json:"response_text"`
	Status                 int     `json:"status"`
	FailureCount           int     `json:"failure_count"`
	FailureStatus          int     `json:"failure_status"`
	FailureMessage         string  `json:"failure_message"`
	Stream                 bool    `json:"stream"`
	StreamDisconnect       bool    `json:"stream_disconnect"`
	StreamWait             bool    `json:"stream_wait"`
	InputTokens            int64   `json:"input_tokens"`
	OutputTokens           int64   `json:"output_tokens"`
	CachedReadTokens       int64   `json:"cached_read_tokens"`
	ReasoningTokens        int64   `json:"reasoning_tokens"`
	Cost                   float64 `json:"cost"`
}

type Request struct {
	OpenCodeSession string `json:"opencode_session,omitempty"`
	Method          string `json:"method"`
	Path            string `json:"path"`
	Body            string `json:"body"`
}

// Fixture is a complete response for one inference request. Fixture files
// keep browser E2E scenarios reviewable and make each upstream response
// explicit instead of hiding it in a mutable test scenario.
type Fixture struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    json.RawMessage   `json:"body"`
}

type Server struct {
	mu            sync.Mutex
	scenario      Scenario
	requests      []Request
	fixtureDir    string
	fixtureNames  []string
	fixtureOffset int
	streamRelease chan struct{}
}

func New(scenario Scenario) *Server {
	return &Server{scenario: normalizeScenario(scenario), streamRelease: make(chan struct{})}
}

func NewWithFixtureDir(scenario Scenario, fixtureDir string) *Server {
	server := New(scenario)
	server.fixtureDir = strings.TrimSpace(fixtureDir)
	return server
}

func normalizeScenario(scenario Scenario) Scenario {
	if scenario.ResponseText == "" {
		scenario.ResponseText = "mock response"
	}
	if scenario.Status == 0 {
		scenario.Status = http.StatusOK
	}
	if scenario.FailureStatus == 0 {
		scenario.FailureStatus = http.StatusServiceUnavailable
	}
	if scenario.FailureMessage == "" {
		scenario.FailureMessage = "mock transient failure"
	}
	return scenario
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	s.mu.Lock()
	s.requests = append(s.requests, Request{Method: r.Method, Path: r.URL.Path, Body: string(body), OpenCodeSession: r.Header.Get("x-opencode-session")})
	scenario := s.scenario
	if scenario.FailureCount > 0 && isInference(r.URL.Path) {
		s.scenario.FailureCount--
	}
	failed := scenario.FailureCount > 0 && isInference(r.URL.Path)
	s.mu.Unlock()

	if strings.HasPrefix(r.URL.Path, "/__mock/") {
		s.handleControl(w, r, body)
		return
	}
	if r.URL.Path == "/healthz" {
		s.writeGeneric(w, r.URL.Path, map[string]any{"status": "ok"})
		return
	}
	if strings.HasSuffix(r.URL.Path, "/models") || strings.HasSuffix(r.URL.Path, "/models/user") {
		s.writeModels(w, scenario)
		return
	}
	if strings.Contains(r.URL.Path, "/markets") || strings.HasSuffix(r.URL.Path, "/prices") {
		s.writeMarketData(w, scenario)
		return
	}
	if !isInference(r.URL.Path) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if scenario.RequireOpenCodeSession && strings.TrimSpace(r.Header.Get("x-opencode-session")) == "" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"Request is missing x-opencode-session and cannot be routed efficiently.","type":"invalid_request"}}`)
		return
	}
	if fixture, ok, err := s.nextFixture(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"message":"mock fixture failed: `+jsonEscape(err.Error())+`"}}`)
		return
	} else if ok {
		s.writeFixture(w, fixture)
		return
	}
	if failed {
		w.WriteHeader(scenario.FailureStatus)
		_, _ = io.WriteString(w, `{"error":{"message":"`+scenario.FailureMessage+`","type":"mock_error"}}`)
		return
	}
	if scenario.Status < 200 || scenario.Status >= 300 {
		w.WriteHeader(scenario.Status)
		_, _ = io.WriteString(w, `{"error":{"message":"`+scenario.FailureMessage+`","type":"mock_error"}}`)
		return
	}
	if scenario.Stream || requestStream(body) {
		s.writeStream(w, r, scenario)
		return
	}
	s.writeInference(w, r.URL.Path, scenario)
}

func requestStream(body []byte) bool {
	var payload struct {
		Stream bool `json:"stream"`
	}
	return json.Unmarshal(body, &payload) == nil && payload.Stream
}

func (s *Server) handleControl(w http.ResponseWriter, r *http.Request, body []byte) {
	switch r.URL.Path {
	case "/__mock/reset":
		s.mu.Lock()
		s.requests = nil
		s.mu.Unlock()
		s.writeGeneric(w, r.URL.Path, map[string]any{"reset": true})
	case "/__mock/fixtures":
		var input struct {
			Files []string `json:"files"`
		}
		if err := json.Unmarshal(body, &input); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		for _, name := range input.Files {
			if err := validateFixtureName(name); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, err.Error())
				return
			}
		}
		s.mu.Lock()
		s.fixtureNames = append([]string(nil), input.Files...)
		s.fixtureOffset = 0
		s.mu.Unlock()
		s.writeGeneric(w, r.URL.Path, map[string]any{"updated": true, "files": len(input.Files)})
	case "/__mock/scenario":
		var scenario Scenario
		if err := json.Unmarshal(body, &scenario); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if scenario.ResponseText == "" {
			scenario.ResponseText = "mock response"
		}
		if scenario.Status == 0 {
			scenario.Status = http.StatusOK
		}
		scenario = normalizeScenario(scenario)
		s.mu.Lock()
		s.scenario = scenario
		s.mu.Unlock()
		s.writeGeneric(w, r.URL.Path, map[string]any{"updated": true})
	case "/__mock/stream/release":
		s.mu.Lock()
		close(s.streamRelease)
		s.streamRelease = make(chan struct{})
		s.mu.Unlock()
		s.writeGeneric(w, r.URL.Path, map[string]any{"released": true})
	case "/__mock/requests":
		s.mu.Lock()
		requests := append([]Request(nil), s.requests...)
		s.mu.Unlock()
		s.writeGeneric(w, r.URL.Path, map[string]any{"data": requests})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (s *Server) writeModels(w http.ResponseWriter, scenario Scenario) {
	data := make([]map[string]any, 0, len(scenario.Models))
	for _, model := range scenario.Models {
		data = append(data, map[string]any{"id": model.ID, "name": model.Name, "free": model.Free, "context_length": model.ContextLength, "max_completion_tokens": model.MaxCompletionTokens, "pricing": map[string]string{"prompt": model.PromptPrice, "completion": model.CompletionPrice}, "architecture": map[string]any{"input_modalities": model.InputModalities, "output_modalities": model.OutputModalities}, "supported_features": model.SupportedFeatures, "tags": model.Tags})
	}
	s.writeGeneric(w, "/models", map[string]any{"data": data})
}

func (s *Server) writeMarketData(w http.ResponseWriter, scenario Scenario) {
	models := make([]map[string]any, 0, len(scenario.Models))
	for _, model := range scenario.Models {
		models = append(models, map[string]any{"model": model.ID, "best_ask": map[string]string{"input": model.PromptPrice, "output": model.CompletionPrice}, "seller_count": 1})
	}
	s.writeGeneric(w, "/markets", map[string]any{"models": models})
}

func (s *Server) writeGeneric(w http.ResponseWriter, _ string, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func (s *Server) writeStream(w http.ResponseWriter, request *http.Request, scenario Scenario) {
	path := request.URL.Path
	// Capture the latch before flushing: the client can release it immediately
	// after receiving the first frame.
	s.mu.Lock()
	release := s.streamRelease
	s.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	chunks := splitStreamText(scenario.ResponseText)
	for index, chunk := range chunks {
		if strings.HasSuffix(path, "/responses") {
			writeSSE(w, flusher, "response.output_text.delta", map[string]any{"type": "response.output_text.delta", "delta": chunk})
		} else if strings.HasSuffix(path, "/messages") {
			writeSSE(w, flusher, "content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": chunk}})
		} else {
			writeSSE(w, flusher, "", map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": chunk}}}})
		}
		if index == 0 && scenario.StreamWait {
			select {
			case <-release:
			case <-request.Context().Done():
				return
			}
		}
	}
	if scenario.StreamDisconnect {
		return
	}
	usage := map[string]any{"input_tokens": scenario.InputTokens, "output_tokens": scenario.OutputTokens, "total_tokens": scenario.InputTokens + scenario.OutputTokens}
	if strings.HasSuffix(path, "/messages") {
		writeSSE(w, flusher, "message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn"}, "usage": usage})
		writeSSE(w, flusher, "message_stop", map[string]any{"type": "message_stop"})
	} else if strings.HasSuffix(path, "/responses") {
		writeSSE(w, flusher, "response.completed", map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed", "usage": usage}})
	} else {
		writeSSE(w, flusher, "", map[string]any{"usage": map[string]any{"prompt_tokens": scenario.InputTokens, "completion_tokens": scenario.OutputTokens, "total_tokens": scenario.InputTokens + scenario.OutputTokens, "prompt_tokens_details": map[string]any{"cached_tokens": scenario.CachedReadTokens}, "completion_tokens_details": map[string]any{"reasoning_tokens": scenario.ReasoningTokens}, "cost": scenario.Cost}})
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}
}

func splitStreamText(text string) []string {
	if len(text) < 2 {
		return []string{text}
	}
	middle := len(text) / 2
	return []string{text[:middle], text[middle:]}
}

func writeSSE(w io.Writer, flusher http.Flusher, event string, payload any) {
	encoded, _ := json.Marshal(payload)
	if event != "" {
		_, _ = fmt.Fprintf(w, "event: %s\n", event)
	}
	_, _ = fmt.Fprintf(w, "data: %s\n\n", encoded)
	if flusher != nil {
		flusher.Flush()
	}
}

func (s *Server) writeJSONResponse(w http.ResponseWriter, path string, scenario Scenario) {
	if strings.HasSuffix(path, "/messages") {
		s.writeGeneric(w, path, map[string]any{"id": "mock-message", "type": "message", "role": "assistant", "content": []any{map[string]any{"type": "text", "text": scenario.ResponseText}}, "stop_reason": "end_turn", "usage": map[string]any{"input_tokens": scenario.InputTokens, "output_tokens": scenario.OutputTokens, "cache_read_input_tokens": scenario.CachedReadTokens}})
		return
	}
	if strings.HasSuffix(path, "/responses") {
		s.writeGeneric(w, path, map[string]any{"id": "mock-response", "object": "response", "status": "completed", "output": []any{map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": scenario.ResponseText}}}}, "usage": map[string]any{"input_tokens": scenario.InputTokens, "output_tokens": scenario.OutputTokens, "total_tokens": scenario.InputTokens + scenario.OutputTokens}})
		return
	}
	s.writeGeneric(w, path, map[string]any{"id": "mock-response", "object": "response", "choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": scenario.ResponseText}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": scenario.InputTokens, "completion_tokens": scenario.OutputTokens, "total_tokens": scenario.InputTokens + scenario.OutputTokens, "cost": scenario.Cost}})
}

func (s *Server) writeInference(w http.ResponseWriter, path string, scenario Scenario) {
	s.writeJSONResponse(w, path, scenario)
}

func (s *Server) nextFixture() (Fixture, bool, error) {
	s.mu.Lock()
	if s.fixtureDir == "" || s.fixtureOffset >= len(s.fixtureNames) {
		s.mu.Unlock()
		return Fixture{}, false, nil
	}
	name := s.fixtureNames[s.fixtureOffset]
	s.fixtureOffset++
	dir := s.fixtureDir
	s.mu.Unlock()

	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return Fixture{}, true, err
	}
	var fixture Fixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		return Fixture{}, true, fmt.Errorf("decode %s: %w", name, err)
	}
	return fixture, true, nil
}

func (s *Server) writeFixture(w http.ResponseWriter, fixture Fixture) {
	status := fixture.Status
	if status == 0 {
		status = http.StatusOK
	}
	for key, value := range fixture.Headers {
		w.Header().Set(key, value)
	}
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(status)
	if len(fixture.Body) > 0 {
		_, _ = w.Write(fixture.Body)
	}
}

func validateFixtureName(name string) error {
	clean := filepath.Clean(strings.TrimSpace(name))
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("invalid mock fixture name %q", name)
	}
	return nil
}

func jsonEscape(value string) string {
	encoded, _ := json.Marshal(value)
	if len(encoded) >= 2 {
		return string(encoded[1 : len(encoded)-1])
	}
	return ""
}

func isInference(path string) bool {
	return strings.HasSuffix(path, "/chat/completions") || strings.HasSuffix(path, "/responses") || strings.HasSuffix(path, "/messages")
}
