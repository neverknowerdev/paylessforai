package controllers

import (
	"net/http"

	"github.com/neverknowerdev/paylessforai/internal/matcher"
)

func (s *Server) registerProxyRoutes(mux *http.ServeMux) {
	for _, path := range []string{"/v1/chat/completions", "/v1/responses", "/v1/messages", "/anthropic/v1/messages"} {
		mux.HandleFunc(path, s.handleProxy)
	}
}

func (s *Server) handleProxy(w http.ResponseWriter, r *http.Request) {
	if s.proxy != nil {
		protocol := matcher.ProtocolChatCompletions
		switch r.URL.Path {
		case "/v1/responses":
			protocol = matcher.ProtocolResponses
		case "/v1/messages", "/anthropic/v1/messages":
			protocol = matcher.ProtocolAnthropic
		}
		s.proxy.ServeHTTP(w, r, protocol)
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "inference endpoint only accepts POST")
		return
	}
	writeError(w, http.StatusNotImplemented, "not_implemented", "provider proxy is not configured yet")
}
