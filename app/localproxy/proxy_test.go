package localproxy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProxyForwardsRequestsAndInjectsConfiguredKey(t *testing.T) {
	oldTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer server-key" {
			t.Errorf("authorization = %q", got)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"object":"list"}`)), Header: make(http.Header)}, nil
	})
	defer func() { http.DefaultTransport = oldTransport }()
	proxy, err := NewHandler(Config{ListenAddr: "127.0.0.1:0", RemoteURL: "http://remote.test", ServerAPIKey: "server-key", ReadHeaderTimeout: 1, IdleTimeout: 1})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	proxy.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "list") {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
	}
}

func TestConfigValidationRequiresHostedServerAndValidListenAddress(t *testing.T) {
	cfg := DefaultConfig()
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected missing server URL error")
	}
	cfg.RemoteURL = "https://gateway.example.com"
	cfg.ListenAddr = "not-an-address"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid listen address error")
	}
}

func TestProxyPreservesClientKey(t *testing.T) {
	oldTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if got := r.Header.Get("Authorization"); got != "Bearer client-key" {
			t.Errorf("authorization = %q", got)
		}
		return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})
	defer func() { http.DefaultTransport = oldTransport }()
	proxy, err := NewHandler(Config{ListenAddr: "127.0.0.1:0", RemoteURL: "http://remote.test", ServerAPIKey: "server-key", ReadHeaderTimeout: 1, IdleTimeout: 1})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	request.Header.Set("Authorization", "Bearer client-key")
	response := httptest.NewRecorder()
	proxy.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestProxyRemoteErrorIsJSON(t *testing.T) {
	oldTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(_ *http.Request) (*http.Response, error) { return nil, fmt.Errorf("connection refused") })
	defer func() { http.DefaultTransport = oldTransport }()
	proxy, err := NewHandler(Config{ListenAddr: "127.0.0.1:0", RemoteURL: "http://remote.test", ReadHeaderTimeout: 1, IdleTimeout: 1})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/models", nil)
	response := httptest.NewRecorder()
	proxy.ServeHTTP(response, request)
	if response.Code != http.StatusBadGateway || !strings.Contains(response.Body.String(), "remote_unavailable") {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
