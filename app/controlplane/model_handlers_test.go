package controlplane

import (
	"testing"

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
