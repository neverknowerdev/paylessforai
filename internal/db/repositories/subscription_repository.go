package repositories

import (
	"context"
	"database/sql"
	"time"

	"github.com/neverknowerdev/paylessforai/internal/subscription"
)

type SubscriptionRepository struct{ database *sql.DB }

func (r *SubscriptionRepository) Usage(ctx context.Context) ([]subscription.UsageRow, error) {
	rows, err := r.database.QueryContext(ctx, `SELECT c.provider,c.label,c.subscription_fee_pico_usd,c.subscription_cycle_start,c.subscription_cycle_end,r.received_at,COALESCE(u.input_tokens,0),COALESCE(u.output_tokens,0) FROM provider_credentials c JOIN proxy_requests r ON r.selected_provider=c.provider LEFT JOIN request_usage u ON u.request_id=r.id WHERE c.access_mode='subscription' AND c.subscription_fee_pico_usd IS NOT NULL AND r.state='succeeded' ORDER BY r.received_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []subscription.UsageRow{}
	for rows.Next() {
		var provider, label, at string
		var start, end sql.NullString
		var fee, input, output int64
		if err := rows.Scan(&provider, &label, &fee, &start, &end, &at, &input, &output); err != nil {
			return nil, err
		}
		row := subscription.UsageRow{Provider: provider, Label: label, FeePicoUSD: fee, InputTokens: input, OutputTokens: output}
		row.At, _ = time.Parse(time.RFC3339Nano, at)
		if start.Valid {
			row.CycleStart, _ = time.Parse(time.RFC3339Nano, start.String)
		}
		if end.Valid {
			row.CycleEnd, _ = time.Parse(time.RFC3339Nano, end.String)
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func (r *SubscriptionRepository) Pricing(ctx context.Context) ([]subscription.Pricing, error) {
	rows, err := r.Usage(ctx)
	if err != nil {
		return nil, err
	}
	return subscription.Calculate(rows), nil
}
