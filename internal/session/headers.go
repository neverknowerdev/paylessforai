package session

import (
	"net/http"
	"strings"
)

const (
	payLessChatIDHeader   = "X-PayLess-Chat-Id"
	openCodeSessionHeader = "x-opencode-session"
	maxHeaderBytes        = 256
)

// selectedHeader returns one valid supported value. A repeated header is an
// unusable signal and is never concatenated.
func selectedHeader(headers http.Header) (string, bool) {
	for _, name := range []string{payLessChatIDHeader, openCodeSessionHeader} {
		values := make([]string, 0, 1)
		for key, entries := range headers {
			if strings.EqualFold(key, name) {
				values = append(values, entries...)
			}
		}
		if len(values) != 1 {
			continue
		}
		value := strings.TrimSpace(values[0])
		if usableHeaderValue(value) {
			return value, true
		}
	}
	return "", false
}

func usableHeaderValue(value string) bool {
	if value == "" || len([]byte(value)) > maxHeaderBytes {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}
