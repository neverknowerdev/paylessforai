package subscription

import (
	"sort"
	"time"
)

type UsageRow struct {
	Provider, Label           string
	FeePicoUSD                int64
	CycleStart, CycleEnd      time.Time
	At                        time.Time
	InputTokens, OutputTokens int64
}

type Pricing struct {
	Provider                         string `json:"provider"`
	Label                            string `json:"label,omitempty"`
	FeePicoUSD                       int64  `json:"fee_pico_usd"`
	InputTokens                      int64  `json:"input_tokens"`
	OutputTokens                     int64  `json:"output_tokens"`
	EffectiveInputPicoUSDPerMillion  *int64 `json:"effective_input_pico_usd_per_million,omitempty"`
	EffectiveOutputPicoUSDPerMillion *int64 `json:"effective_output_pico_usd_per_million,omitempty"`
	Observed5HMinPicoUSDPerMillion   *int64 `json:"observed_5h_min_pico_usd_per_million,omitempty"`
	Observed5HMaxPicoUSDPerMillion   *int64 `json:"observed_5h_max_pico_usd_per_million,omitempty"`
	Explanation                      string `json:"explanation"`
}

func Calculate(rows []UsageRow) []Pricing {
	groups := map[string][]UsageRow{}
	for _, row := range rows {
		if row.FeePicoUSD > 0 {
			groups[row.Provider] = append(groups[row.Provider], row)
		}
	}
	result := make([]Pricing, 0, len(groups))
	for provider, items := range groups {
		p := Pricing{Provider: provider, Label: items[0].Label, FeePicoUSD: items[0].FeePicoUSD, Explanation: "Subscription fee is allocated across observed tokens; input and output use the same blended rate to avoid double-counting. The 5h range reflects observed non-empty windows, not a theoretical quota price."}
		var in, out int64
		for _, row := range items {
			in += row.InputTokens
			out += row.OutputTokens
		}
		p.InputTokens, p.OutputTokens = in, out
		if total := in + out; total > 0 {
			value := (p.FeePicoUSD/total)*1_000_000 + (p.FeePicoUSD%total)*1_000_000/total
			p.EffectiveInputPicoUSDPerMillion = &value
			p.EffectiveOutputPicoUSDPerMillion = &value
		}
		buckets := map[time.Time]int64{}
		for _, row := range items {
			start := row.CycleStart
			if start.IsZero() {
				start = row.At
			}
			end := row.CycleEnd
			if end.IsZero() {
				end = start.AddDate(0, 1, 0)
			}
			if end.Before(start) {
				end = start.AddDate(0, 1, 0)
			}
			elapsed := row.At.Sub(start)
			if elapsed < 0 {
				elapsed = 0
			}
			bucket := start.Add((elapsed / (5 * time.Hour)) * 5 * time.Hour)
			if bucket.Before(end) {
				buckets[bucket] += row.InputTokens + row.OutputTokens
			}
		}
		var min, max int64
		first := true
		for _, tokens := range buckets {
			if tokens == 0 {
				continue
			}
			duration := 5 * time.Hour
			cycle := items[0].CycleEnd.Sub(items[0].CycleStart)
			if cycle <= 0 {
				cycle = 30 * 24 * time.Hour
			}
			cost := p.FeePicoUSD * int64(duration) / int64(cycle)
			rate := cost * 1_000_000 / tokens
			if first || rate < min {
				min = rate
			}
			if first || rate > max {
				max = rate
			}
			first = false
		}
		if !first {
			p.Observed5HMinPicoUSDPerMillion = &min
			p.Observed5HMaxPicoUSDPerMillion = &max
		}
		result = append(result, p)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Provider < result[j].Provider })
	return result
}
