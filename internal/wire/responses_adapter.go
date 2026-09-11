package wire

import "net/http"

type responsesAdapter struct{}

func (responsesAdapter) Format() Format { return FormatResponses }
func (responsesAdapter) DecodeRequest(body []byte) (*Request, error) {
	return decodeRequest(FormatResponses, body)
}
func (responsesAdapter) EncodeRequest(req *Request) ([]byte, error) { return encodeResponses(req) }
func (responsesAdapter) DecodeResponse(resp *http.Response) (EventStream, error) {
	return decodeResponse(FormatResponses, resp)
}
func (responsesAdapter) EncodeResponse(events EventStream, dst http.ResponseWriter) error {
	return encodeResponse(FormatResponses, events, dst)
}
