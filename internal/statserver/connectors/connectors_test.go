package connectors

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestDefaultConnectorDefinitions(t *testing.T) {
	items := Default("aa", "or", "hf", "surplus")
	if len(items) != 4 {
		t.Fatalf("count=%d", len(items))
	}
	for _, item := range items {
		if item.Name == "" || item.DisplayName == "" || item.URL == "" || item.Fetch == nil {
			t.Fatalf("invalid connector=%+v", item)
		}
	}
}

func TestNumberConversions(t *testing.T) {
	if number(1.5) != 1.5 || number(json.Number("2.5")) != 2.5 || number("3.5") != 3.5 || number("bad") != 0 {
		t.Fatal("number conversion failed")
	}
	if pointer := millionPointer("0.000001"); pointer == nil || *pointer != 1 {
		t.Fatalf("million pointer=%v", pointer)
	}
}

func TestGetJSONHeadersAndErrors(t *testing.T) {
	oldClient := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("authorization=%q", request.Header.Get("Authorization"))
		}
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"m"}]}`)), Request: request}, nil
	})}
	defer func() { http.DefaultClient = oldClient }()
	var response struct {
		Data []map[string]string `json:"data"`
	}
	if err := getJSON(context.Background(), "https://fixture.test/models", "token", "Authorization", &response); err != nil || len(response.Data) != 1 || response.Data[0]["id"] != "m" {
		t.Fatalf("response=%v err=%v", response, err)
	}
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 418, Status: "418 I'm a teapot", Header: make(http.Header), Body: io.NopCloser(strings.NewReader("nope")), Request: request}, nil
	})}
	if err := getJSON(context.Background(), "https://fixture.test/error", "", "", &response); err == nil || !strings.Contains(err.Error(), "418") {
		t.Fatalf("error=%v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }
