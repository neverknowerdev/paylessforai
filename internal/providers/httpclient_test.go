package providers

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/neverknowerdev/paylessforai/internal/matcher"
	"github.com/neverknowerdev/paylessforai/internal/retry"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestDiscoverAndParsePricing(t *testing.T) {
	client := NewHTTPClient("openrouter", "https://provider.invalid/v1", "key")
	client.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/models/user" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"model-a","name":"Model A","context_length":1000,"pricing":{"prompt":"0.000001","completion":"0.000002","cacheRead":"0.0000002","cacheWrite":"0.0000005","reasoning":"0.000003","request":"0.00001"},"supported_parameters":["tools"],"architecture":{"input_modalities":["text","image"],"output_modalities":["text"]},"supported_features":["streaming","tools"]}]}`)), Header: make(http.Header), Request: r}, nil
	})}
	models, err := client.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || !models[0].PriceAvailable || models[0].Pricing.InputPicoUSDPerToken != 1_000_000 || models[0].Pricing.CachedReadPicoUSDPerToken != 200_000 || models[0].Pricing.FixedPicoUSD != 10_000_000 {
		t.Fatalf("unexpected models: %#v", models)
	}
	if len(models[0].InputModalities) != 2 || models[0].InputModalities[1] != "image" || len(models[0].Tags) != 2 {
		t.Fatalf("unexpected model metadata: %#v", models[0])
	}
}

func TestDiscoverRecognizesFreeVariantWithoutPricing(t *testing.T) {
	client := NewHTTPClient("openrouter", "https://provider.invalid/v1", "key")
	client.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"model-a:free","name":"Model A Free"}]}`)), Header: make(http.Header), Request: r}, nil
	})}
	models, err := client.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || !models[0].Free || !models[0].PriceAvailable || models[0].Pricing.InputPicoUSDPerToken != 0 || models[0].Pricing.OutputPicoUSDPerToken != 0 {
		t.Fatalf("unexpected free model: %#v", models)
	}
}

func TestDiscoverMapsTopLevelModalityTags(t *testing.T) {
	client := NewHTTPClient("surplus", "https://provider.invalid/v1", "key")
	client.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"model-a","pricing":{"prompt":"0.000001","completion":"0.000002"},"tags":["text","video","audio"]}]}`)), Header: make(http.Header), Request: r}, nil
	})}
	models, err := client.Discover(context.Background())
	if err != nil || len(models) != 1 {
		t.Fatalf("unexpected models: %#v, %v", models, err)
	}
	if len(models[0].InputModalities) != 3 || len(models[0].Tags) != 3 || models[0].OutputModalities[0] != "text" {
		t.Fatalf("unexpected modality tags: %#v", models[0])
	}
}

func TestDiscoverDoesNotCallMediaMeteredZeroTokenModelFree(t *testing.T) {
	client := NewHTTPClient("surplus", "https://provider.invalid/v1", "key")
	client.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"image-model","pricing":{"prompt":"0","completion":"0","image":"0.36"},"architecture":{"modality":"text->image"}}]}`)), Header: make(http.Header), Request: r}, nil
	})}
	models, err := client.Discover(context.Background())
	if err != nil || len(models) != 1 {
		t.Fatalf("unexpected models: %#v, %v", models, err)
	}
	if models[0].Free {
		t.Fatalf("media-metered model was classified as free: %#v", models[0])
	}
}

func TestDiscoverRequiresExplicitFreeLabelForSurplusZeroTokenModel(t *testing.T) {
	client := NewHTTPClient("surplus", "https://provider.invalid/v1", "key")
	client.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"zero-token-model","name":"Zero Token Model","pricing":{"prompt":"0","completion":"0"}}]}`)), Header: make(http.Header), Request: r}, nil
	})}
	models, err := client.Discover(context.Background())
	if err != nil || len(models) != 1 || models[0].Free {
		t.Fatalf("bare zero token pricing must not imply free: %#v, %v", models, err)
	}
}

func TestDiscoverDoesNotCallNonzeroSurplusFreeLabelFree(t *testing.T) {
	client := NewHTTPClient("surplus", "https://provider.invalid/v1", "key")
	client.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"model-a:free","name":"Model A Free","pricing":{"prompt":"0.000001","completion":"0.000002"}}]}`)), Header: make(http.Header), Request: r}, nil
	})}
	models, err := client.Discover(context.Background())
	if err != nil || len(models) != 1 || models[0].Free {
		t.Fatalf("nonzero surplus free-labelled route must not be free: %#v, %v", models, err)
	}
}

func TestDiscoverAcceptsNumericPricingValues(t *testing.T) {
	client := NewHTTPClient("surplus", "https://provider.invalid/v1", "key")
	client.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"model-a","pricing":{"prompt":0.000001,"completion":0.000002}}]}`)), Header: make(http.Header), Request: r}, nil
	})}
	models, err := client.Discover(context.Background())
	if err != nil || len(models) != 1 || !models[0].PriceAvailable || models[0].Pricing.OutputPicoUSDPerToken != 2_000_000 {
		t.Fatalf("unexpected numeric pricing: %#v, %v", models, err)
	}
}

func TestDiscoverUsesSurplusMarketDiscountedAndOfficialPrices(t *testing.T) {
	client := NewHTTPClient("surplus", "https://provider.invalid/v1", "key")
	client.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/v1/models" {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"model-a","pricing":{"prompt":"0.000000200","completion":"0.000000900"}}]}`)), Header: make(http.Header), Request: r}, nil
		}
		if r.URL.Path == "/api/markets" {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"markets":[{"model":"model-a","best_input_per_1m":12000,"best_output_per_1m":54000,"direct_input_per_1m":200000,"direct_output_per_1m":900000}]}`)), Header: make(http.Header), Request: r}, nil
		}
		t.Fatalf("unexpected path %s", r.URL.Path)
		return nil, nil
	})}
	models, err := client.Discover(context.Background())
	if err != nil || len(models) != 1 {
		t.Fatalf("unexpected models: %#v, %v", models, err)
	}
	model := models[0]
	if model.Pricing.InputPicoUSDPerToken != 12000 || model.Pricing.OutputPicoUSDPerToken != 54000 || model.OfficialPricing.InputPicoUSDPerToken != 200000 || model.OfficialPricing.OutputPicoUSDPerToken != 900000 || !model.OfficialPriceAvailable {
		t.Fatalf("market prices were not applied: %#v", model)
	}
}

func TestDoRewritesModelAndClassifiesErrors(t *testing.T) {
	client := NewHTTPClient("openrouter", "https://provider.invalid/v1", "key")
	client.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer key" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL)
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"model":"upstream"`) {
			t.Fatalf("model was not rewritten: %s", body)
		}
		return &http.Response{StatusCode: http.StatusTooManyRequests, Body: io.NopCloser(strings.NewReader("slow down")), Header: make(http.Header), Request: r}, nil
	})}
	_, err := client.Do(context.Background(), matcher.ProtocolChatCompletions, "upstream", []byte(`{"model":"logical","messages":[]}`))
	if err == nil {
		t.Fatal("expected error")
	}
	upstreamErr, ok := err.(*UpstreamError)
	if !ok || upstreamErr.Class != retry.ErrorRateLimit {
		t.Fatalf("unexpected error: %#v", err)
	}
}
