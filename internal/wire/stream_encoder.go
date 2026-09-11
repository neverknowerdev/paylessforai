package wire

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// startStream initializes a downstream SSE response.
func startStream(format Format, w http.ResponseWriter) (http.Flusher, error) {
	if err := format.Validate(); err != nil {
		return nil, err
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	if format == FormatResponses {
		if err := writeSSEFrame(w, flusher, "response.created", map[string]any{"type": "response.created", "response": map[string]any{"id": "resp-translation", "object": "response", "status": "in_progress"}}); err != nil {
			return flusher, err
		}
		if err := writeSSEFrame(w, flusher, "response.output_item.added", map[string]any{"type": "response.output_item.added", "item": map[string]any{"id": "item-translation", "type": "message", "role": "assistant"}}); err != nil {
			return flusher, err
		}
		if err := writeSSEFrame(w, flusher, "response.content_part.added", map[string]any{"type": "response.content_part.added", "item_id": "item-translation", "part": map[string]any{"type": "output_text", "text": ""}}); err != nil {
			return flusher, err
		}
	}
	if format == FormatAnthropicMessages {
		if err := writeSSEFrame(w, flusher, "message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": "msg-translation", "type": "message", "role": "assistant", "content": []any{}, "model": "translation"}}); err != nil {
			return flusher, err
		}
		if err := writeSSEFrame(w, flusher, "content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}}); err != nil {
			return flusher, err
		}
	}
	return flusher, nil
}

// encodeStreamEvent writes one protocol-specific SSE event and flushes it.
func encodeStreamEvent(format Format, event Event, w http.ResponseWriter, flusher http.Flusher) error {
	var payload any
	switch format {
	case FormatChatCompletions:
		payload = encodeChatStreamEvent(event)
	case FormatResponses:
		payload = encodeResponsesStreamEvent(event)
	case FormatAnthropicMessages:
		payload = encodeAnthropicStreamEvent(event)
	default:
		return format.Validate()
	}
	if payload == nil {
		return nil
	}
	name := ""
	if format != FormatChatCompletions {
		if object, ok := payload.(map[string]any); ok {
			name, _ = object["type"].(string)
		}
	}
	return writeSSEFrame(w, flusher, name, payload)
}

func writeSSEFrame(w io.Writer, flusher http.Flusher, event string, payload any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	frame := ""
	if event != "" {
		frame += "event: " + event + "\n"
	}
	frame += "data: " + string(encoded) + "\n\n"
	if _, err := io.WriteString(w, frame); err != nil {
		return err
	}
	if flusher != nil {
		flusher.Flush()
	}
	return nil
}

func endStream(format Format, w http.ResponseWriter, flusher http.Flusher, response Response) error {
	if format == FormatChatCompletions {
		if _, err := io.WriteString(w, "data: [DONE]\n\n"); err != nil {
			return err
		}
		if flusher != nil {
			flusher.Flush()
		}
		return nil
	}
	if format == FormatAnthropicMessages {
		if err := writeSSEFrame(w, flusher, "content_block_stop", map[string]any{"type": "content_block_stop", "index": 0}); err != nil {
			return err
		}
		if err := writeSSEFrame(w, flusher, "message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn"}}); err != nil {
			return err
		}
	}
	if format == FormatResponses {
		if err := writeSSEFrame(w, flusher, "response.output_text.done", map[string]any{"type": "response.output_text.done", "item_id": "item-translation", "text": response.Text}); err != nil {
			return err
		}
		if err := writeSSEFrame(w, flusher, "response.content_part.done", map[string]any{"type": "response.content_part.done", "item_id": "item-translation"}); err != nil {
			return err
		}
		if err := writeSSEFrame(w, flusher, "response.output_item.done", map[string]any{"type": "response.output_item.done", "item": map[string]any{"id": "item-translation", "type": "message", "role": "assistant"}}); err != nil {
			return err
		}
		response.FinishReason = "completed"
		return encodeStreamEvent(format, Event{Type: EventResponse, Response: &response}, w, flusher)
	}
	return encodeStreamEvent(format, Event{Type: EventComplete}, w, flusher)
}

// StreamWriter owns downstream commitment and defers response snapshots and
// usage until the terminal lifecycle, so metadata never ends a stream early.
type StreamWriter struct {
	format    Format
	writer    http.ResponseWriter
	flusher   http.Flusher
	committed bool
	response  Response
	text      strings.Builder
}

func NewStreamWriter(format Format, writer http.ResponseWriter) (*StreamWriter, error) {
	if err := format.Validate(); err != nil {
		return nil, err
	}
	return &StreamWriter{format: format, writer: writer}, nil
}

func (s *StreamWriter) Committed() bool { return s.committed }

func (s *StreamWriter) start() error {
	if s.committed {
		return nil
	}
	// startStream writes headers before its first frame can fail.
	s.committed = true
	var err error
	s.flusher, err = startStream(s.format, s.writer)
	return err
}

func (s *StreamWriter) Write(event Event) error {
	if event.Usage != nil {
		mergeStreamUsage(&s.response.Usage, *event.Usage)
	}
	if event.Response != nil {
		previousUsage := s.response.Usage
		s.response = *event.Response
		s.response.Usage = previousUsage
		mergeStreamUsage(&s.response.Usage, event.Response.Usage)
	}
	if err := s.start(); err != nil {
		return err
	}
	switch event.Type {
	case EventResponse, EventComplete:
		return nil
	case EventUsage:
		if s.format == FormatResponses || event.Usage == nil {
			return nil
		}
	case EventTextDelta:
		if s.format == FormatResponses {
			s.text.WriteString(event.Text)
		}
	}
	return encodeStreamEvent(s.format, event, s.writer, s.flusher)
}

func (s *StreamWriter) End() error {
	if err := s.start(); err != nil {
		return err
	}
	if s.response.Text == "" {
		s.response.Text = s.text.String()
	}
	return endStream(s.format, s.writer, s.flusher, s.response)
}

// WriteResponse expands a buffered JSON result into deltas before retaining
// its snapshot. Incremental snapshots go through Write to avoid duplicate text.
func (s *StreamWriter) WriteResponse(response Response) error {
	if err := s.Write(Event{Type: EventResponse, Response: &response}); err != nil {
		return err
	}
	text := response.Text
	if text == "" {
		for _, message := range response.Messages {
			text += blocksText(message.Content)
		}
	}
	if text != "" {
		if err := s.Write(Event{Type: EventTextDelta, Text: text}); err != nil {
			return err
		}
	}
	for _, message := range response.Messages {
		for _, reasoning := range message.Reasoning {
			if err := s.Write(Event{Type: EventReasoningDelta, Reasoning: reasoning.Text}); err != nil {
				return err
			}
		}
		for _, call := range message.ToolCalls {
			if err := s.Write(Event{Type: EventToolCallDelta, ToolCall: &call}); err != nil {
				return err
			}
		}
	}
	return s.Write(Event{Type: EventUsage, Usage: &response.Usage})
}

func mergeStreamUsage(dst *Usage, src Usage) {
	if src.InputTokens != 0 {
		dst.InputTokens = src.InputTokens
	}
	if src.OutputTokens != 0 {
		dst.OutputTokens = src.OutputTokens
	}
	if src.CachedReadTokens != 0 {
		dst.CachedReadTokens = src.CachedReadTokens
	}
	if src.CacheWriteTokens != 0 {
		dst.CacheWriteTokens = src.CacheWriteTokens
	}
	if src.ReasoningTokens != 0 {
		dst.ReasoningTokens = src.ReasoningTokens
	}
	dst.InputTokensNetOfCache = dst.InputTokensNetOfCache || src.InputTokensNetOfCache
	total := dst.InputTokens + dst.OutputTokens
	if dst.InputTokensNetOfCache {
		total += dst.CachedReadTokens + dst.CacheWriteTokens
	}
	dst.TotalTokens = max(dst.TotalTokens, src.TotalTokens, total)
}

func encodeStreamFor(format Format, events EventStream, w http.ResponseWriter) error {
	stream, err := NewStreamWriter(format, w)
	if err != nil {
		return err
	}
	for _, event := range events.Events {
		if err := stream.Write(event); err != nil {
			return err
		}
	}
	return stream.End()
}
