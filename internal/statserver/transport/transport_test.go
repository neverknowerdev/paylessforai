package transport

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseLimit(t *testing.T) {
	if parseLimit("") != 100 || parseLimit("0") != 100 || parseLimit("999") != 500 || parseLimit("12") != 12 {
		t.Fatal("limit bounds are wrong")
	}
}
func TestMiddlewareAddsSecurityHeadersAndRecovers(t *testing.T) {
	handler := Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("test") }), Recovery, SecurityHeaders)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != 500 || response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("status=%d headers=%v", response.Code, response.Header())
	}
}
func TestJSONResponse(t *testing.T) {
	response := httptest.NewRecorder()
	JSON(response, 201, map[string]string{"ok": "yes"})
	if response.Code != 201 || !strings.Contains(response.Body.String(), `"ok":"yes"`) {
		t.Fatalf("response=%s", response.Body.String())
	}
}
