package controlplane

import (
	"context"
	"encoding/json"
	"github.com/neverknowerdev/paylessforai/internal/catalog"
	"github.com/neverknowerdev/paylessforai/internal/providers"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/neverknowerdev/paylessforai/internal/matcher"
)

func TestCatalogDiscountsCompareRoutesWithOpenRouterBaseline(t *testing.T) {
	routes := []matcher.Route{
		{Provider: "openrouter", LogicalModel: "model-a", UpstreamModel: "model-a", PriceAvailable: true, Price: matcher.Price{InputPicoUSDPerToken: 1_000_000, OutputPicoUSDPerToken: 2_000_000}},
		{Provider: "surplus", LogicalModel: "model-a", UpstreamModel: "model-a", PriceAvailable: true, Price: matcher.Price{InputPicoUSDPerToken: 500_000, OutputPicoUSDPerToken: 1_000_000}},
		{Provider: "openrouter", LogicalModel: "model-a", UpstreamModel: "model-a:free", Free: true, PriceAvailable: true},
	}
	discounts := catalogDiscounts(routes)
	paid, ok := discounts["surplus\x00model-a\x00model-a"]
	if !ok || paid.InputBPS != 5000 || paid.OutputBPS != 5000 || paid.MaxBPS != 5000 {
		t.Fatalf("unexpected paid route discount: %#v", discounts)
	}
	free, ok := discounts["openrouter\x00model-a\x00model-a:free"]
	if !ok || free.MaxBPS != 10000 || free.InputBPS != 10000 || free.OutputBPS != 10000 {
		t.Fatalf("unexpected free route discount: %#v", discounts)
	}
}

func TestCatalogDiscountsRequireOfficialBaseline(t *testing.T) {
	routes := []matcher.Route{{Provider: "surplus", LogicalModel: "model-a", UpstreamModel: "model-a", PriceAvailable: true, Price: matcher.Price{InputPicoUSDPerToken: 1, OutputPicoUSDPerToken: 1}}}
	if discounts := catalogDiscounts(routes); len(discounts) != 0 {
		t.Fatalf("route without OpenRouter baseline received discount: %#v", discounts)
	}
}

func TestCatalogDiscountsClampOverpricedRoutesAndUseProviderBaseline(t *testing.T) {
	routes := []matcher.Route{
		{Provider: "surplus", LogicalModel: "market-model", UpstreamModel: "market-model", PriceAvailable: true, OfficialPriceAvailable: true, Price: matcher.Price{InputPicoUSDPerToken: 200, OutputPicoUSDPerToken: 50}, OfficialPrice: matcher.Price{InputPicoUSDPerToken: 100, OutputPicoUSDPerToken: 100}},
	}
	discounts := catalogDiscounts(routes)
	item, ok := discounts["surplus\x00market-model\x00market-model"]
	if !ok || item.InputBPS != 0 || item.OutputBPS != 5000 || item.MaxBPS != 5000 || item.OfficialInput != 100 || item.Source != "surplus" {
		t.Fatalf("unexpected provider baseline discount: %#v", discounts)
	}
}

type discoveryTestClient struct{ providers.Client }

func (discoveryTestClient) Name() string { return "surplus" }
func (discoveryTestClient) Discover(context.Context) ([]providers.Model, error) {
	return []providers.Model{{ID: "recent"}, {ID: "expired"}}, nil
}

type discoveryTestStore struct{ value string }

func (s discoveryTestStore) Get(context.Context, string) (string, bool, error) {
	return s.value, true, nil
}
func (discoveryTestStore) Set(context.Context, string, string) error { return nil }

func TestCatalogModelsHighlightsOnlyRecentPersistedDiscoveries(t *testing.T) {
	manager := catalog.New([]providers.Client{discoveryTestClient{}})
	history, err := json.Marshal(map[string]any{
		"known":     map[string]bool{"recent": true, "expired": true},
		"additions": []catalog.Addition{{ID: "recent", AddedAt: time.Now().Add(-time.Hour)}, {ID: "expired", AddedAt: time.Now().Add(-catalog.NewModelWindow - time.Hour)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Restore(context.Background(), discoveryTestStore{string(history)}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	server := &Server{catalog: manager}
	response := httptest.NewRecorder()
	server.handleCatalogModels(response, httptest.NewRequest(http.MethodGet, "/api/models", nil))
	var payload struct {
		Data []struct {
			Model   string `json:"model"`
			IsNew   bool   `json:"is_new"`
			AddedAt string `json:"added_at"`
		}
		UpdatedAt time.Time `json:"updated_at"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Data) != 2 || payload.UpdatedAt.IsZero() {
		t.Fatalf("invalid payload: %s", response.Body.String())
	}
	for _, model := range payload.Data {
		if model.IsNew != (model.Model == "recent") {
			t.Fatalf("incorrect new flag: %#v", model)
		}
		if model.IsNew && model.AddedAt == "" {
			t.Fatal("missing discovery date")
		}
	}
}
