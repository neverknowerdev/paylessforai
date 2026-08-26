package db

import (
	"context"
	"database/sql"
	"time"

	"github.com/neverknowerdev/paylessforai/internal/subscription"
)

// subscriptionUsage is a cross-table reporting query. Table repositories do
// not own joins across provider credentials, requests, and usage records.
func (s *Store) subscriptionUsage(ctx context.Context) ([]subscription.UsageRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT c.provider,c.label,c.subscription_fee_pico_usd,c.subscription_cycle_start,c.subscription_cycle_end,r.received_at,COALESCE(u.input_tokens,0),COALESCE(u.output_tokens,0) FROM provider_credentials c JOIN proxy_requests r ON r.selected_provider=c.provider LEFT JOIN request_usage u ON u.request_id=r.id WHERE c.access_mode='subscription' AND c.subscription_fee_pico_usd IS NOT NULL AND r.state='succeeded' ORDER BY r.received_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []subscription.UsageRow{}
	for rows.Next() {
		var provider, label, receivedAt string
		var fee int64
		var cycleStart, cycleEnd sql.NullString
		var inputTokens, outputTokens int64
		if err := rows.Scan(&provider, &label, &fee, &cycleStart, &cycleEnd, &receivedAt, &inputTokens, &outputTokens); err != nil {
			return nil, err
		}
		when, err := time.Parse(time.RFC3339Nano, receivedAt)
		if err != nil {
			continue
		}
		usage := subscription.UsageRow{Provider: provider, Label: label, FeePicoUSD: fee, At: when, InputTokens: inputTokens, OutputTokens: outputTokens}
		if cycleStart.Valid {
			usage.CycleStart, _ = time.Parse(time.RFC3339Nano, cycleStart.String)
		}
		if cycleEnd.Valid {
			usage.CycleEnd, _ = time.Parse(time.RFC3339Nano, cycleEnd.String)
		}
		out = append(out, usage)
	}
	return out, rows.Err()
}
