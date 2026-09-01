package repositories

import (
	"context"
	"database/sql"
	"time"

	bobmodels "github.com/neverknowerdev/paylessforai/internal/db/bob/models"
	"github.com/neverknowerdev/paylessforai/internal/subscription"
	"github.com/stephenafamo/bob"
	"github.com/stephenafamo/bob/dialect/sqlite"
	"github.com/stephenafamo/bob/dialect/sqlite/sm"
	"github.com/stephenafamo/scan"
)

type SubscriptionRepository struct{ bobRepository }

type subscriptionUsageRecord struct {
	Provider     string           `db:"provider"`
	Label        string           `db:"label"`
	Fee          int64            `db:"subscription_fee_pico_usd"`
	CycleStart   sql.Null[string] `db:"subscription_cycle_start"`
	CycleEnd     sql.Null[string] `db:"subscription_cycle_end"`
	ReceivedAt   string           `db:"received_at"`
	InputTokens  int64            `db:"input_tokens"`
	OutputTokens int64            `db:"output_tokens"`
}

func (r *SubscriptionRepository) Usage(ctx context.Context) ([]subscription.UsageRow, error) {
	query := sqlite.Select(
		sm.Columns(
			bobmodels.ProviderCredentials.Columns.Provider.As("provider"),
			bobmodels.ProviderCredentials.Columns.Label.As("label"),
			bobmodels.ProviderCredentials.Columns.SubscriptionFeePicoUsd.As("subscription_fee_pico_usd"),
			bobmodels.ProviderCredentials.Columns.SubscriptionCycleStart.As("subscription_cycle_start"),
			bobmodels.ProviderCredentials.Columns.SubscriptionCycleEnd.As("subscription_cycle_end"),
			bobmodels.ProxyRequests.Columns.ReceivedAt.As("received_at"),
			sqlite.F("COALESCE", bobmodels.RequestUsages.Columns.InputTokens, sqlite.Arg(0))().As("input_tokens"),
			sqlite.F("COALESCE", bobmodels.RequestUsages.Columns.OutputTokens, sqlite.Arg(0))().As("output_tokens"),
		),
		sm.From(bobmodels.ProviderCredentials.NameAsExpr()),
		sm.InnerJoin(bobmodels.ProxyRequests.NameAsExpr()).OnEQ(bobmodels.ProxyRequests.Columns.SelectedProvider, bobmodels.ProviderCredentials.Columns.Provider),
		sm.LeftJoin(bobmodels.RequestUsages.NameAsExpr()).OnEQ(bobmodels.RequestUsages.Columns.RequestID, bobmodels.ProxyRequests.Columns.ID),
		sm.Where(sqlite.And(
			bobmodels.ProviderCredentials.Columns.AccessMode.EQ(sqlite.Arg("subscription")),
			bobmodels.ProviderCredentials.Columns.SubscriptionFeePicoUsd.IsNotNull(),
			bobmodels.ProxyRequests.Columns.State.EQ(sqlite.Arg("succeeded")),
		)),
		sm.OrderBy(bobmodels.ProxyRequests.Columns.ReceivedAt),
	)
	records, err := bob.All(ctx, r.exec, query, scan.StructMapper[subscriptionUsageRecord]())
	if err != nil {
		return nil, err
	}
	result := make([]subscription.UsageRow, 0, len(records))
	for _, record := range records {
		row := subscription.UsageRow{Provider: record.Provider, Label: record.Label, FeePicoUSD: record.Fee, InputTokens: record.InputTokens, OutputTokens: record.OutputTokens}
		row.At, _ = time.Parse(time.RFC3339Nano, record.ReceivedAt)
		if record.CycleStart.Valid {
			row.CycleStart, _ = time.Parse(time.RFC3339Nano, record.CycleStart.V)
		}
		if record.CycleEnd.Valid {
			row.CycleEnd, _ = time.Parse(time.RFC3339Nano, record.CycleEnd.V)
		}
		result = append(result, row)
	}
	return result, nil
}

func (r *SubscriptionRepository) Pricing(ctx context.Context) ([]subscription.Pricing, error) {
	rows, err := r.Usage(ctx)
	if err != nil {
		return nil, err
	}
	return subscription.Calculate(rows), nil
}
