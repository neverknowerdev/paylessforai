// Package mockprovider provides a deterministic OpenAI/Anthropic-compatible
// upstream for integration and browser E2E tests. It never contacts an LLM.
package mockprovider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

type Model struct {
	ID                  string   `json:"id"`
	Name                string   `json:"name"`
	ContextLength       int64    `json:"context_length"`
	MaxCompletionTokens int64    `json:"max_completion_tokens"`
	PromptPrice         string   `json:"prompt_price"`
	CompletionPrice     string   `json:"completion_price"`
	SupportedParameters []string `json:"supported_parameters"`
}

type Scenario struct {
	Models           []Model `json:"models"`
	ResponseText     string  `json:"response_text"`
	Status           int     `json:"status"`
	FailureCount     int     `json:"failure_count"`
	FailureStatus    int     `json:"failure_status"`
	Stream           bool    `json:"stream"`
	StreamDisconnect bool    `json:"stream_disconnect"`
	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	CachedReadTokens int64   `json:"cached_read_tokens"`
	ReasoningTokens  int64   `json:"reasoning_tokens"`
	Cost             float64 `json:"cost"`
}

type Request struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Body   string `json:"body"`
}

type Server struct {
	mu       sync.Mutex
	scenario Scenario
	requests []Request
}

func New(scenario Scenario) *Server {
	return &Server{scenario: normalizeScenario(scenario)}
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
	return scenario
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	s.mu.Lock()
	s.requests = append(s.requests, Request{Method: r.Method, Path: r.URL.Path, Body: string(body)})
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
	if failed {
		w.WriteHeader(scenario.FailureStatus)
		_, _ = io.WriteString(w, `{"error":{"message":"mock transient failure","type":"mock_error"}}`)
		return
	}
	if scenario.Status < 200 || scenario.Status >= 300 {
		w.WriteHeader(scenario.Status)
		_, _ = io.WriteString(w, `{"error":{"message":"mock configured failure","type":"mock_error"}}`)
		return
	}
	if scenario.Stream || requestStream(body) {
		s.writeStream(w, scenario)
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
		data = append(data, map[string]any{"id": model.ID, "name": model.Name, "context_length": model.ContextLength, "max_completion_tokens": model.MaxCompletionTokens, "pricing": map[string]string{"prompt": model.PromptPrice, "completion": model.CompletionPrice}, "supported_parameters": model.SupportedParameters})
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

func (s *Server) writeStream(w http.ResponseWriter, scenario Scenario) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"delta":{"content":"`+scenario.ResponseText+`"}}]}`)
	if flusher != nil {
		flusher.Flush()
	}
	if scenario.StreamDisconnect {
		return
	}
	usage := fmt.Sprintf(`{"usage":{"prompt_tokens":%d,"completion_tokens":%d,"total_tokens":%d,"prompt_tokens_details":{"cached_tokens":%d},"completion_tokens_details":{"reasoning_tokens":%d},"cost":%g}}`, scenario.InputTokens, scenario.OutputTokens, scenario.InputTokens+scenario.OutputTokens, scenario.CachedReadTokens, scenario.ReasoningTokens, scenario.Cost)
	_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", usage)
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

func isInference(path string) bool {
	return strings.HasSuffix(path, "/chat/completions") || strings.HasSuffix(path, "/responses") || strings.HasSuffix(path, "/messages")
}
