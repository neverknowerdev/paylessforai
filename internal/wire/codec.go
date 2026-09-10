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

type codec struct{ format Format }

func (c codec) Format() Format                              { return c.format }
func (c codec) DecodeRequest(body []byte) (*Request, error) { return DecodeRequest(c.format, body) }
func (c codec) EncodeRequest(req *Request) ([]byte, error)  { return EncodeRequest(c.format, req) }
func (c codec) DecodeResponse(resp *http.Response) (EventStream, error) {
	return DecodeResponse(c.format, resp)
}
func (c codec) EncodeResponse(events EventStream, dst http.ResponseWriter) error {
	return EncodeResponse(c.format, events, dst)
}

func CodecFor(format Format) (Codec, error) {
	if err := format.Validate(); err != nil {
		return nil, err
	}
	return codec{format: format}, nil
}

func MustCodec(format Format) Codec {
	result, err := CodecFor(format)
	if err != nil {
		panic(err)
	}
	return result
}
