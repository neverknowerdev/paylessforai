package wire

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func sseResponse(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func TestStreamDecodersShareFramingAndValidation(t *testing.T) {
	cases := []struct {
		name    string
		format  Format
		body    string
		count   int
		wantErr bool
	}{
		{"multiline CRLF", FormatChatCompletions, ": keepalive\r\nevent: message\r\ndata: {\"choices\":\r\ndata: [{\"delta\":{\"content\":\"hi\"}}]}\r\n\r\ndata: [DONE]\r\n\r\n", 1, false},
		{"nested Anthropic usage", FormatAnthropicMessages, "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":7}}}\n\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":3}}\n\ndata: {\"type\":\"message_stop\"}\n\n", 2, false},
		{"named Responses lifecycle", FormatResponses, "event: response.output_text.delta\ndata: {\"delta\":\"hi\"}\n\nevent: response.completed\ndata: {\"response\":{\"output_text\":\"hi\",\"usage\":{\"input_tokens\":7,\"output_tokens\":3}}}\n\n", 3, false},
		{"failed Responses lifecycle", FormatResponses, "data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"message\":\"failed upstream\"}}}\n\n", 1, true},
		{"incomplete Responses lifecycle", FormatResponses, "event: response.incomplete\ndata: {\"response\":{\"usage\":{\"output_tokens\":3}}}\n\n", 2, true},
		{"truncated", FormatChatCompletions, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n", 1, true},
		{"malformed", FormatChatCompletions, "data: nope\n\n", 0, true},
		{"null payload", FormatChatCompletions, "data: null\n\n", 0, true},
		{"unrecognized Anthropic payload", FormatAnthropicMessages, "data: {\"ok\":true}\n\ndata: {\"type\":\"message_stop\"}\n\n", 0, true},
		{"wrong terminal protocol", FormatChatCompletions, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: {\"type\":\"message_stop\"}\n\n", 1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var incremental []Event
			count, err := StreamResponse(tc.format, sseResponse(tc.body), func(event Event) error { incremental = append(incremental, event); return nil })
			if (err != nil) != tc.wantErr || count != tc.count {
				t.Fatalf("count=%d err=%v", count, err)
			}
			buffered, bufferedErr := DecodeResponse(tc.format, sseResponse(tc.body))
			if (bufferedErr != nil) != tc.wantErr || !reflect.DeepEqual(incremental, buffered.Events) {
				t.Fatalf("buffered events=%+v err=%v; incremental=%+v", buffered.Events, bufferedErr, incremental)
			}
			if tc.name == "nested Anthropic usage" && (incremental[0].Usage.InputTokens != 7 || incremental[1].Usage.OutputTokens != 3) {
				t.Fatalf("nested usage lost: %+v", incremental)
			}
			if count > 0 && tc.wantErr {
				var partial *PartialResponseError
				if !errors.As(err, &partial) {
					t.Fatalf("missing partial error: %v", err)
				}
			}
		})
	}
}

func TestResponsesStreamCompletesOnceAfterFinalContent(t *testing.T) {
	recorder := httptest.NewRecorder()
	stream, err := NewStreamWriter(FormatResponses, recorder)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range []Event{
		{Type: EventUsage, Usage: &Usage{InputTokens: 7}},
		{Type: EventTextDelta, Text: "hello"},
		{Type: EventUsage, Usage: &Usage{OutputTokens: 3}},
		{Type: EventResponse, Response: &Response{Text: "hello", Usage: Usage{InputTokens: 7, OutputTokens: 3}}},
	} {
		if err := stream.Write(event); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Contains(recorder.Body.String(), "response.completed") {
		t.Fatal("metadata completed the stream early")
	}
	if err := stream.End(); err != nil {
		t.Fatal(err)
	}
	body := recorder.Body.String()
	if strings.Count(body, "event: response.completed\n") != 1 {
		t.Fatalf("duplicate completion: %s", body)
	}
	if strings.Count(body, "event: response.output_text.delta\n") != 1 {
		t.Fatalf("snapshot repeated text: %s", body)
	}
	events, err := DecodeResponse(FormatResponses, sseResponse(body))
	if err != nil {
		t.Fatal(err)
	}
	response := events.Events[len(events.Events)-1].Response
	if response == nil || response.Text != "hello" || response.Usage.InputTokens != 7 || response.Usage.OutputTokens != 3 || response.Usage.TotalTokens != 10 {
		t.Fatalf("incomplete terminal snapshot: %+v", response)
	}
	if strings.LastIndex(body, "event: response.completed\n") < strings.LastIndex(body, "event: response.output_item.done\n") {
		t.Fatal("completion precedes output lifecycle")
	}
}

type rejectingStreamWriter struct{ *httptest.ResponseRecorder }

func (w rejectingStreamWriter) WriteString(value string) (int, error) { return w.Write([]byte(value)) }

func (w rejectingStreamWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestStreamWriterTracksCommitBeforeFirstFrameFails(t *testing.T) {
	for _, format := range []Format{FormatChatCompletions, FormatResponses, FormatAnthropicMessages} {
		t.Run(string(format), func(t *testing.T) {
			stream, err := NewStreamWriter(format, rejectingStreamWriter{httptest.NewRecorder()})
			if err != nil {
				t.Fatal(err)
			}
			err = stream.Write(Event{Type: EventTextDelta, Text: "hi"})
			if !errors.Is(err, io.ErrClosedPipe) || !stream.Committed() {
				t.Fatalf("committed=%v error=%v", stream.Committed(), err)
			}
		})
	}
}

func TestBufferedStreamEncodingPropagatesWriteFailure(t *testing.T) {
	err := EncodeResponse(FormatResponses, EventStream{Stream: true, Events: []Event{{Type: EventTextDelta, Text: "hi"}}}, rejectingStreamWriter{httptest.NewRecorder()})
	if !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("write error swallowed: %v", err)
	}
}

func TestStreamResponseDeliversBeforeUpstreamCompletes(t *testing.T) {
	reader, writer := io.Pipe()
	release := make(chan struct{})
	go func() {
		_, _ = io.WriteString(writer, "data: {\"choices\":[{\"delta\":{\"content\":\"first\"}}]}\n\n")
		<-release
		_, _ = io.WriteString(writer, "data: [DONE]\n\n")
		_ = writer.Close()
	}()
	response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: reader}
	got := make(chan Event, 1)
	done := make(chan error, 1)
	go func() {
		_, err := StreamResponse(FormatChatCompletions, response, func(event Event) error {
			got <- event
			return nil
		})
		done <- err
	}()
	select {
	case event := <-got:
		if event.Type != EventTextDelta || event.Text != "first" {
			t.Fatalf("event = %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("first stream event was buffered until upstream completion")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestEndStreamUsesProtocolSpecificTerminal(t *testing.T) {
	for _, format := range []Format{FormatChatCompletions, FormatResponses, FormatAnthropicMessages} {
		t.Run(string(format), func(t *testing.T) {
			recorder := httptest.NewRecorder()
			stream, err := NewStreamWriter(format, recorder)
			if err != nil {
				t.Fatal(err)
			}
			if err := stream.End(); err != nil {
				t.Fatal(err)
			}
			body := recorder.Body.String()
			if format == FormatChatCompletions && !strings.Contains(body, "[DONE]") {
				t.Fatalf("missing Chat terminal: %s", body)
			}
			if format != FormatChatCompletions && strings.Contains(body, "[DONE]") {
				t.Fatalf("non-Chat stream used Chat terminal: %s", body)
			}
		})
	}
}
