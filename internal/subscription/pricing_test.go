package subscription

import (
	"testing"
	"time"
)

func TestCalculateBlendedAndFiveHourRange(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	got := Calculate([]UsageRow{{Provider: "x", FeePicoUSD: 20_000_000_000_000, CycleStart: start, CycleEnd: start.Add(30 * 24 * time.Hour), At: start.Add(time.Hour), InputTokens: 1_000_000}, {Provider: "x", FeePicoUSD: 20_000_000_000_000, CycleStart: start, CycleEnd: start.Add(30 * 24 * time.Hour), At: start.Add(6 * time.Hour), OutputTokens: 2_000_000}})
	if len(got) != 1 || got[0].InputTokens != 1_000_000 || got[0].OutputTokens != 2_000_000 {
		t.Fatalf("unexpected %+v", got)
	}
	if got[0].EffectiveInputPicoUSDPerMillion == nil || *got[0].EffectiveInputPicoUSDPerMillion != 6_666_666_666_666 {
		t.Fatalf("blended price %d", *got[0].EffectiveInputPicoUSDPerMillion)
	}
	if got[0].Observed5HMinPicoUSDPerMillion == nil || got[0].Observed5HMaxPicoUSDPerMillion == nil {
		t.Fatal("expected 5h range")
	}
}
