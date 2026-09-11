package wire

import "net/http"

type chatAdapter struct{}

func (chatAdapter) Format() Format { return FormatChatCompletions }
func (chatAdapter) DecodeRequest(body []byte) (*Request, error) {
	return decodeRequest(FormatChatCompletions, body)
}
func (chatAdapter) EncodeRequest(req *Request) ([]byte, error) { return encodeChat(req) }
func (chatAdapter) DecodeResponse(resp *http.Response) (EventStream, error) {
	return decodeResponse(FormatChatCompletions, resp)
}
func (chatAdapter) EncodeResponse(events EventStream, dst http.ResponseWriter) error {
	return encodeResponse(FormatChatCompletions, events, dst)
}
