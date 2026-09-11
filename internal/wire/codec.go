package wire

import (
	"net/http"
)

// Codec is the stable boundary between a public protocol and the canonical
// semantic representation. Implementations are stateless and safe to reuse.
type Codec interface {
	Format() Format
	DecodeRequest(body []byte) (*Request, error)
	EncodeRequest(req *Request) ([]byte, error)
	DecodeResponse(resp *http.Response) (EventStream, error)
	EncodeResponse(events EventStream, dst http.ResponseWriter) error
}

func CodecFor(format Format) (Codec, error) {
	if err := format.Validate(); err != nil {
		return nil, err
	}
	switch format {
	case FormatChatCompletions:
		return chatAdapter{}, nil
	case FormatResponses:
		return responsesAdapter{}, nil
	case FormatAnthropicMessages:
		return anthropicAdapter{}, nil
	default:
		return nil, format.Validate()
	}
}

func MustCodec(format Format) Codec {
	result, err := CodecFor(format)
	if err != nil {
		panic(err)
	}
	return result
}
