package wire

import "net/http"

type anthropicAdapter struct{}

func (anthropicAdapter) Format() Format { return FormatAnthropicMessages }
func (anthropicAdapter) DecodeRequest(body []byte) (*Request, error) {
	return decodeRequest(FormatAnthropicMessages, body)
}
func (anthropicAdapter) EncodeRequest(req *Request) ([]byte, error) { return encodeAnthropic(req) }
func (anthropicAdapter) DecodeResponse(resp *http.Response) (EventStream, error) {
	return decodeResponse(FormatAnthropicMessages, resp)
}
func (anthropicAdapter) EncodeResponse(events EventStream, dst http.ResponseWriter) error {
	return encodeResponse(FormatAnthropicMessages, events, dst)
}
