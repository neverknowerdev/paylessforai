package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/neverknowerdev/paylessforai/internal/catalog"
	"github.com/neverknowerdev/paylessforai/internal/matcher"
	"github.com/neverknowerdev/paylessforai/internal/providers"
)

type catalogModelTestClient struct {
	provider string
	model    providers.Model
}

func (c catalogModelTestClient) Name() string { return c.provider }
func (c catalogModelTestClient) Discover(context.Context) ([]providers.Model, error) {
	return []providers.Model{c.model}, nil
}
func (c catalogModelTestClient) Do(context.Context, matcher.Protocol, string, []byte, string) (*http.Response, error) {
	return nil, nil
}

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

func TestCatalogModelsEndpointReturnsCanonicalModelName(t *testing.T) {
	server, cleanup := testServer(t)
	defer cleanup()
	modelCatalog := catalog.New([]providers.Client{
		catalogModelTestClient{provider: "opencode", model: providers.Model{ID: "muse-spark-1.3-contributor-free"}},
		catalogModelTestClient{provider: "openrouter", model: providers.Model{ID: "meta/muse-spark-1.3-contributor"}},
	})
	if err := modelCatalog.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	server.catalog = modelCatalog
	response := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/models", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"model":"muse-spark-1.3-contributor"`) || !strings.Contains(response.Body.String(), `"name":"Muse Spark 1.3 Contributor"`) {
		t.Fatalf("unexpected canonical catalog response: %d %s", response.Code, response.Body.String())
	}
}

func TestCatalogModelsEndpointReturnsRouteUsageAndFreeTag(t *testing.T) {
	server, cleanup := testServer(t)
	defer cleanup()
	modelCatalog := catalog.New([]providers.Client{
		catalogModelTestClient{provider: "surplus", model: providers.Model{ID: "model-a:free", Free: true}},
		catalogModelTestClient{provider: "openrouter", model: providers.Model{ID: "model-a"}},
	})
	if err := modelCatalog.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	server.catalog = modelCatalog
	if err := server.db.ProxyRequests.Create(context.Background(), "catalog-usage", "", "chat.completions", "model-a"); err != nil {
		t.Fatal(err)
	}
	if err := server.db.ProxyRequests.RecordAttemptRoute(context.Background(), "catalog-usage", 1, "surplus", "model-a:free"); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/models", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d %s", response.Code, response.Body.String())
	}
	var payload struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, item := range payload.Data {
		if item["provider"] != "surplus" {
			continue
		}
		found = true
		if item["usage_7d"] != float64(1) || item["free"] != true {
			t.Fatalf("unexpected route metadata: %#v", item)
		}
		tags, ok := item["tags"].([]any)
		if !ok || len(tags) != 1 || tags[0] != "free" {
			t.Fatalf("free tag missing: %#v", item["tags"])
		}
	}
	if !found {
		t.Fatalf("surplus route missing: %#v", payload.Data)
	}
}

func TestIsNewCatalogAdditionUsesTwentyFourHourWindow(t *testing.T) {
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	if !isNewCatalogAddition(now.Add(-23*time.Hour-59*time.Minute), now) {
		t.Fatal("addition inside the 24-hour window was not marked new")
	}
	if isNewCatalogAddition(now.Add(-24*time.Hour), now) {
		t.Fatal("addition at the 24-hour boundary was still marked new")
	}
}
