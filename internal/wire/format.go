package wire

import "fmt"

// Format identifies an HTTP inference wire format. The empty value is the
// persisted/discovery representation of an unknown upstream format; it is
// never valid for an encoded network request.
type Format string

const (
	FormatUnknown           Format = ""
	FormatChatCompletions   Format = "openai_chat_completions"
	FormatResponses         Format = "openai_responses"
	FormatAnthropicMessages Format = "anthropic_messages"
)

func (f Format) String() string { return string(f) }

func (f Format) Valid() bool {
	switch f {
	case FormatChatCompletions, FormatResponses, FormatAnthropicMessages:
		return true
	default:
		return false
	}
}

func (f Format) Validate() error {
	if !f.Valid() {
		return fmt.Errorf("unsupported wire format %q", f)
	}
	return nil
}

// FormatForProtocol maps the public ingress protocol to the canonical wire
// format. matcher.Protocol is deliberately not used here so upstream routing
// cannot accidentally become protocol pass-through again.
func FormatForProtocol(protocol string) Format {
	switch protocol {
	case "chat_completions":
		return FormatChatCompletions
	case "responses":
		return FormatResponses
	case "anthropic_messages":
		return FormatAnthropicMessages
	default:
		return FormatUnknown
	}
}
