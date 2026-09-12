package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/neverknowerdev/paylessforai/internal/wire"
)

const structuredProbeSchema = `{"type":"object","properties":{"value":{"type":"string","enum":["ok"]}},"required":["value"],"additionalProperties":false}`

// StructuredOutputUnsupportedError means the endpoint was reachable but did
// not accept or return the requested structured output. Callers may safely
// persist this negative capability result; transport, auth, quota, and billing
// failures remain ordinary errors and should be retried later.
type StructuredOutputUnsupportedError struct{ Err error }

func (e *StructuredOutputUnsupportedError) Error() string {
	return "structured output unsupported: " + e.Err.Error()
}
func (e *StructuredOutputUnsupportedError) Unwrap() error { return e.Err }

// ProbeStructuredOutput performs a small, non-streaming request against the
// model's real endpoint. A successful response must contain exactly the
// schema-conforming JSON object; a provider error or malformed response is a
// failed probe. The probe deliberately uses the same translation adapters as
// normal requests so protocol support is tested end to end.
func (c *HTTPClient) ProbeStructuredOutput(ctx context.Context, model Model) error {
	strict := true
	maxTokens := int64(8)
	request := &wire.Request{
		Model:    model.ID,
		Messages: []wire.Message{{Role: wire.RoleUser, Content: []wire.ContentBlock{{Type: "text", Text: "Return the requested object."}}}},
		Options: wire.RequestOptions{
			MaxOutputTokens: &maxTokens,
			Temperature:     floatPointer(0),
			StructuredOutput: &wire.StructuredOutput{
				Type: "json_schema", Name: "structured_probe", Schema: json.RawMessage(structuredProbeSchema), Strict: &strict,
			},
		},
	}

	candidates := CandidateFormats(model.Format, c.endpoint.HintedFormat, wire.FormatChatCompletions)
	var failures []error
	for _, format := range candidates {
		encoded, err := wire.EncodeRequest(format, request)
		if err != nil {
			if !wire.IsIncompatibility(err) {
				return fmt.Errorf("%s encode: %w", format, err)
			}
			failures = append(failures, fmt.Errorf("%s encode: %w", format, err))
			continue
		}
		prepared, err := c.Prepare(format, model.ID, encoded, "")
		if err != nil {
			return fmt.Errorf("%s prepare: %w", format, err)
		}
		response, err := c.DoPrepared(ctx, prepared)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s request: %w", format, err))
			if !probeShouldTryNextFormat(err) {
				return fmt.Errorf("%s request: %w", format, err)
			}
			continue
		}
		events, err := wire.DecodeResponse(format, response)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s decode: %w", format, err))
			var malformed *wire.MalformedResponseError
			if !errors.As(err, &malformed) && !wire.IsIncompatibility(err) {
				return fmt.Errorf("%s decode: %w", format, err)
			}
			continue
		}
		if err := validateStructuredProbe(events); err != nil {
			failures = append(failures, fmt.Errorf("%s output: %w", format, err))
			continue
		}
		return nil
	}
	if len(failures) == 0 {
		return fmt.Errorf("structured output probe had no usable provider format")
	}
	return &StructuredOutputUnsupportedError{Err: fmt.Errorf("structured output probe failed: %v", failures)}
}

// probeShouldTryNextFormat treats endpoint and payload-shape responses as
// format evidence. Authentication, quota, rate-limit, timeout, and billing
// errors are provider-state failures; retrying those with another format
// would send unnecessary duplicate requests.
func probeShouldTryNextFormat(err error) bool {
	if wire.IsIncompatibility(err) {
		return true
	}
	var malformed *wire.MalformedResponseError
	if errors.As(err, &malformed) {
		return true
	}
	var upstream *UpstreamError
	if !errors.As(err, &upstream) {
		return false
	}
	switch upstream.StatusCode {
	case http.StatusBadRequest,
		http.StatusNotFound,
		http.StatusMethodNotAllowed,
		http.StatusNotAcceptable,
		http.StatusUnsupportedMediaType,
		http.StatusUnprocessableEntity:
		return true
	default:
		return upstream.StatusCode >= http.StatusInternalServerError && upstream.StatusCode <= 599
	}
}

func floatPointer(value float64) *float64 { return &value }

func validateStructuredProbe(events wire.EventStream) error {
	var text string
	for _, event := range events.Events {
		if event.Response != nil {
			text += event.Response.Text
			for _, message := range event.Response.Messages {
				for _, block := range message.Content {
					if block.Type == "text" {
						text += block.Text
					}
				}
			}
		}
		if event.Type == wire.EventTextDelta {
			text += event.Text
		}
	}
	var object map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader([]byte(text)))
	if err := decoder.Decode(&object); err != nil {
		return fmt.Errorf("response is not a JSON object: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("response contains trailing JSON values")
	}
	if len(object) != 1 {
		return fmt.Errorf("response has %d fields, want exactly one", len(object))
	}
	var value string
	if err := json.Unmarshal(object["value"], &value); err != nil || value != "ok" {
		return fmt.Errorf("response value is not %q", "ok")
	}
	return nil
}
