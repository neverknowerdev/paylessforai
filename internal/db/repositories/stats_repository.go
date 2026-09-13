package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	bobmodels "github.com/neverknowerdev/paylessforai/internal/db/bob/models"
	"github.com/neverknowerdev/paylessforai/internal/db/models"
	"github.com/stephenafamo/bob"
	"github.com/stephenafamo/bob/dialect/sqlite"
	"github.com/stephenafamo/bob/dialect/sqlite/sm"
	"github.com/stephenafamo/scan"
)

// StatsRepository reads the persisted request, usage, and attempt models with
// Bob, then performs the reporting aggregates in Go. This keeps reporting
// portable and avoids duplicating raw SQL column names in the repository.
type StatsRepository struct{ bobRepository }

type RequestStat = models.RequestStat
type AttemptStat = models.AttemptStat
type StatsSummary = models.StatsSummary
type ModelStats = models.ModelStats
type ProviderStats = models.ProviderStats
type GroupStats = models.GroupStats

type statsData struct {
	requests  []*bobmodels.ProxyRequest
	usage     map[string]*bobmodels.RequestUsage
	attempts  map[string][]*bobmodels.ProxyAttempt
	groups    map[string]*bobmodels.RoutingGroup
	providers map[string]string
}

// RouteUsageSince counts requests by the provider and upstream model selected
// for the request. A request is counted once, even when routing retried it.
// The selected route is the final route recorded by the proxy request.
func (r *StatsRepository) RouteUsageSince(ctx context.Context, since time.Time) (map[string]int64, error) {
	if r == nil || r.exec == nil {
		return nil, fmt.Errorf("database unavailable")
	}
	query := sqlite.Select(
		sm.Columns(
			bobmodels.ProxyRequests.Columns.SelectedProvider.As("provider"),
			bobmodels.ProxyRequests.Columns.SelectedUpstreamModel.As("upstream_model"),
			sqlite.Raw("COUNT(*)").As("requests"),
		),
		sm.From(bobmodels.ProxyRequests.NameAsExpr()),
		sm.Where(bobmodels.ProxyRequests.Columns.ReceivedAt.GTE(sqlite.Arg(since.UTC().Format(time.RFC3339Nano)))),
		sm.GroupBy(sqlite.Raw("\"selected_provider\", \"selected_upstream_model\"")),
	)
	type routeUsageRow struct {
		Provider      sql.Null[string]
		UpstreamModel sql.Null[string]
		Requests      int64
	}
	rows, err := bob.All(ctx, r.exec, query, scan.StructMapper[routeUsageRow]())
	if err != nil {
		return nil, err
	}
	result := make(map[string]int64, len(rows))
	for _, row := range rows {
		if row.Provider.Valid && row.Provider.V != "" && row.UpstreamModel.Valid && row.UpstreamModel.V != "" {
			result[row.Provider.V+"\x00"+row.UpstreamModel.V] = row.Requests
		}
	}
	return result, nil
}

type requestSummaryAggregate struct {
	TotalRequests         int64         `db:"total_requests"`
	SucceededRequests     int64         `db:"succeeded_requests"`
	FailedRequests        int64         `db:"failed_requests"`
	PartialRequests       int64         `db:"partial_requests"`
	TotalAttempts         int64         `db:"total_attempts"`
	RetriedRequests       int64         `db:"retried_requests"`
	RequestsWithTime      int64         `db:"requests_with_time"`
	FastestMS             sql.NullInt64 `db:"fastest_ms"`
	SlowestMS             sql.NullInt64 `db:"slowest_ms"`
	AverageMS             sql.NullInt64 `db:"average_ms"`
	EligibleRequests      int64         `db:"eligible_requests"`
	ExcludedLimitRequests int64         `db:"excluded_limit_requests"`
}

type usageSummaryAggregate struct {
	InputTokens        int64 `db:"input_tokens"`
	OutputTokens       int64 `db:"output_tokens"`
	TotalTokens        int64 `db:"total_tokens"`
	CachedReadTokens   int64 `db:"cached_read_tokens"`
	CacheWriteTokens   int64 `db:"cache_write_tokens"`
	ReasoningTokens    int64 `db:"reasoning_tokens"`
	EstimatedCostPico  int64 `db:"estimated_cost_pico_usd"`
	OfficialCostPico   int64 `db:"official_cost_pico_usd"`
	ActualCostPico     int64 `db:"actual_cost_pico_usd"`
	SavedCostPico      int64 `db:"saved_cost_pico_usd"`
	RequestsWithActual int64 `db:"requests_with_actual_cost"`
}

func (r *StatsRepository) loadRequestData(ctx context.Context) (statsData, error) {
	if r == nil || r.exec == nil {
		return statsData{}, fmt.Errorf("database unavailable")
	}
	requests, err := bobmodels.ProxyRequests.Query().All(ctx, r.exec)
	if err != nil {
		return statsData{}, err
	}
	usageRows, err := bobmodels.RequestUsages.Query().All(ctx, r.exec)
	if err != nil {
		return statsData{}, err
	}
	attemptRows, err := bobmodels.ProxyAttempts.Query().All(ctx, r.exec)
	if err != nil {
		return statsData{}, err
	}
	data := statsData{requests: requests, usage: make(map[string]*bobmodels.RequestUsage, len(usageRows)), attempts: make(map[string][]*bobmodels.ProxyAttempt)}
	for _, row := range usageRows {
		data.usage[row.RequestID] = row
	}
	for _, row := range attemptRows {
		data.attempts[row.RequestID] = append(data.attempts[row.RequestID], row)
	}
	for requestID := range data.attempts {
		sort.Slice(data.attempts[requestID], func(i, j int) bool {
			return data.attempts[requestID][i].AttemptNumber < data.attempts[requestID][j].AttemptNumber
		})
	}
	return data, nil
}

func (r *StatsRepository) load(ctx context.Context) (statsData, error) {
	data, err := r.loadRequestData(ctx)
	if err != nil {
		return statsData{}, err
	}
	groupRows, err := bobmodels.RoutingGroups.Query().All(ctx, r.exec)
	if err != nil {
		return statsData{}, err
	}
	credentialRows, err := bobmodels.ProviderCredentials.Query().All(ctx, r.exec)
	if err != nil {
		return statsData{}, err
	}
	data.groups = make(map[string]*bobmodels.RoutingGroup, len(groupRows))
	data.providers = make(map[string]string, len(credentialRows))
	for _, row := range groupRows {
		data.groups[row.ID] = row
	}
	for _, row := range credentialRows {
		data.providers[row.ID] = row.Provider
	}
	return data, nil
}

// GroupStats aggregates requests by the routing group that resolved them.
// Requests that were sent directly to a model are intentionally omitted: this
// view answers how each configured group is performing.
func (r *StatsRepository) GroupStats(ctx context.Context) ([]GroupStats, error) {
	data, err := r.load(ctx)
	if err != nil {
		return nil, err
	}
	type aggregate struct {
		item      GroupStats
		durations []int64
	}
	byGroup := make(map[string]*aggregate)
	for _, request := range data.requests {
		if !request.ResolvedGroupID.Valid || request.ResolvedGroupID.V == "" {
			continue
		}
		groupID := request.ResolvedGroupID.V
		entry := byGroup[groupID]
		if entry == nil {
			item := GroupStats{GroupID: groupID, Group: groupID}
			if group := data.groups[groupID]; group != nil {
				item.Group, item.Slug = group.Name, group.Slug
			}
			entry = &aggregate{item: item}
			byGroup[groupID] = entry
		}
		item := &entry.item
		item.Requests++
		if request.StatsDisposition == "included" {
			item.EligibleRequests++
		} else if request.StatsDisposition == "excluded_limit" {
			item.ExcludedLimitRequests++
		}
		switch request.State {
		case "succeeded":
			item.SucceededRequests++
		case "failed":
			item.FailedRequests++
		case "partial":
			item.PartialRequests++
		}
		item.TotalAttempts += request.AttemptCount
		if request.AttemptCount > 1 {
			item.RetriedRequests++
		}
		if request.DurationMS.Valid {
			item.RequestsWithTime++
			entry.durations = append(entry.durations, request.DurationMS.V)
		}
		if usage := data.usage[request.ID]; usage != nil {
			item.InputTokens += usage.InputTokens
			item.OutputTokens += usage.OutputTokens
			item.TotalTokens += usage.TotalTokens
			item.CachedReadTokens += usage.CachedReadTokens
			item.CacheWriteTokens += usage.CacheWriteTokens
			item.ReasoningTokens += usage.ReasoningTokens
			item.EstimatedCostPico += usage.EstimatedCostPicoUsd
			item.OfficialCostPico += usage.OfficialCostPicoUsd
			if usage.ActualCostPicoUsd.Valid {
				item.ActualCostPico += usage.ActualCostPicoUsd.V
			}
			if usage.DiscountPicoUsd.Valid && usage.DiscountPicoUsd.V > 0 {
				item.SavedCostPico += usage.DiscountPicoUsd.V
			}
		}
	}
	result := make([]GroupStats, 0, len(byGroup))
	for _, entry := range byGroup {
		item := entry.item
		if item.Requests > 0 {
			if item.EligibleRequests > 0 {
				item.SuccessRateBPS = item.SucceededRequests * 10000 / item.EligibleRequests
			}
			item.RetryRateBPS = item.RetriedRequests * 10000 / item.Requests
		}
		if item.OfficialCostPico > 0 {
			value := item.SavedCostPico * 10000 / item.OfficialCostPico
			if value < 0 {
				value = 0
			}
			if value > 10000 {
				value = 10000
			}
			item.DiscountBPS = &value
		}
		if len(entry.durations) > 0 {
			min, max, total := entry.durations[0], entry.durations[0], int64(0)
			for _, value := range entry.durations {
				if value < min {
					min = value
				}
				if value > max {
					max = value
				}
				total += value
			}
			average := total / int64(len(entry.durations))
			item.FastestMS, item.SlowestMS, item.AverageMS = &min, &max, &average
		}
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Requests == result[j].Requests {
			return result[i].Group < result[j].Group
		}
		return result[i].Requests > result[j].Requests
	})
	return result, nil
}

func (r *StatsRepository) ListRequestStats(ctx context.Context, limit int) ([]RequestStat, error) {
	items, _, err := r.ListRequestStatsPage(ctx, limit, 0)
	return items, err
}

// ListRequestStatsPage returns the newest request statistics for one page.
// Related usage and attempt rows are fetched only for the returned requests;
// the requests endpoint must not materialize the entire request history.
func (r *StatsRepository) ListRequestStatsPage(ctx context.Context, limit, offset int) ([]RequestStat, bool, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	if r == nil || r.exec == nil {
		return nil, false, fmt.Errorf("database unavailable")
	}

	// Fetch one sentinel row so callers can render a load-more affordance
	// without issuing a COUNT(*) over a potentially large history.
	requests, err := bobmodels.ProxyRequests.Query(
		sm.OrderBy(bobmodels.ProxyRequests.Columns.ReceivedAt).Desc(),
		sm.OrderBy(bobmodels.ProxyRequests.Columns.ID).Desc(),
		sm.Limit(limit+1),
		sm.Offset(offset),
	).All(ctx, r.exec)
	if err != nil {
		return nil, false, err
	}
	hasMore := len(requests) > limit
	if hasMore {
		requests = requests[:limit]
	}

	if len(requests) == 0 {
		return []RequestStat{}, false, nil
	}
	requestIDs := make([]string, 0, len(requests))
	for _, request := range requests {
		requestIDs = append(requestIDs, request.ID)
	}
	idArgs := make([]any, len(requestIDs))
	for i, id := range requestIDs {
		idArgs[i] = id
	}
	usageRows, err := bobmodels.RequestUsages.Query(
		sm.Where(bobmodels.RequestUsages.Columns.RequestID.In(sqlite.Arg(idArgs...))),
	).All(ctx, r.exec)
	if err != nil {
		return nil, false, err
	}
	attemptRows, err := bobmodels.ProxyAttempts.Query(
		sm.Where(bobmodels.ProxyAttempts.Columns.RequestID.In(sqlite.Arg(idArgs...))),
	).All(ctx, r.exec)
	if err != nil {
		return nil, false, err
	}
	credentialRows, err := bobmodels.ProviderCredentials.Query().All(ctx, r.exec)
	if err != nil {
		return nil, false, err
	}
	usage := make(map[string]*bobmodels.RequestUsage, len(usageRows))
	for _, row := range usageRows {
		usage[row.RequestID] = row
	}
	attempts := make(map[string][]*bobmodels.ProxyAttempt, len(requests))
	for _, row := range attemptRows {
		attempts[row.RequestID] = append(attempts[row.RequestID], row)
	}
	for requestID := range attempts {
		sort.Slice(attempts[requestID], func(i, j int) bool {
			return attempts[requestID][i].AttemptNumber < attempts[requestID][j].AttemptNumber
		})
	}
	providers := make(map[string]string, len(credentialRows))
	for _, row := range credentialRows {
		providers[row.ID] = row.Provider
	}

	result := make([]RequestStat, 0, len(requests))
	for _, request := range requests {
		item := requestStatFromBob(request, usage[request.ID])
		for _, attempt := range attempts[request.ID] {
			item.AttemptDetails = append(item.AttemptDetails, attemptStatFromBob(attempt))
		}
		item.SkippedRoutes = skippedRoutesFromPlan(request.ResolvedPlanJSON, providers)
		result = append(result, item)
	}
	return result, hasMore, nil
}

func (r *StatsRepository) RequestStatsSummary(ctx context.Context) (StatsSummary, error) {
	if r == nil || r.exec == nil {
		return StatsSummary{}, fmt.Errorf("database unavailable")
	}
	requestQuery := sqlite.Select(
		sm.Columns(
			sqlite.Raw("COUNT(*)").As("total_requests"),
			sqlite.Raw("COALESCE(SUM(CASE WHEN state = \x27succeeded\x27 THEN 1 ELSE 0 END), 0)").As("succeeded_requests"),
			sqlite.Raw("COALESCE(SUM(CASE WHEN state = \x27failed\x27 THEN 1 ELSE 0 END), 0)").As("failed_requests"),
			sqlite.Raw("COALESCE(SUM(CASE WHEN state = \x27partial\x27 THEN 1 ELSE 0 END), 0)").As("partial_requests"),
			sqlite.Raw("COALESCE(SUM(attempt_count), 0)").As("total_attempts"),
			sqlite.Raw("COALESCE(SUM(CASE WHEN attempt_count > 1 THEN 1 ELSE 0 END), 0)").As("retried_requests"),
			sqlite.Raw("COUNT(duration_ms)").As("requests_with_time"),
			sqlite.Raw("MIN(duration_ms)").As("fastest_ms"),
			sqlite.Raw("MAX(duration_ms)").As("slowest_ms"),
			sqlite.Raw("CAST(AVG(duration_ms) AS INTEGER)").As("average_ms"),
			sqlite.Raw("COALESCE(SUM(CASE WHEN stats_disposition = \x27included\x27 THEN 1 ELSE 0 END), 0)").As("eligible_requests"),
			sqlite.Raw("COALESCE(SUM(CASE WHEN stats_disposition = \x27excluded_limit\x27 THEN 1 ELSE 0 END), 0)").As("excluded_limit_requests"),
		),
		sm.From(bobmodels.ProxyRequests.NameAsExpr()),
	)
	requestTotals, err := bob.One(ctx, r.exec, requestQuery, scan.StructMapper[requestSummaryAggregate]())
	if err != nil {
		return StatsSummary{}, err
	}
	usageQuery := sqlite.Select(
		sm.Columns(
			sqlite.Raw("COALESCE(SUM(input_tokens), 0)").As("input_tokens"),
			sqlite.Raw("COALESCE(SUM(output_tokens), 0)").As("output_tokens"),
			sqlite.Raw("COALESCE(SUM(total_tokens), 0)").As("total_tokens"),
			sqlite.Raw("COALESCE(SUM(cached_read_tokens), 0)").As("cached_read_tokens"),
			sqlite.Raw("COALESCE(SUM(cache_write_tokens), 0)").As("cache_write_tokens"),
			sqlite.Raw("COALESCE(SUM(reasoning_tokens), 0)").As("reasoning_tokens"),
			sqlite.Raw("COALESCE(SUM(estimated_cost_pico_usd), 0)").As("estimated_cost_pico_usd"),
			sqlite.Raw("COALESCE(SUM(official_cost_pico_usd), 0)").As("official_cost_pico_usd"),
			sqlite.Raw("COALESCE(SUM(actual_cost_pico_usd), 0)").As("actual_cost_pico_usd"),
			sqlite.Raw("COALESCE(SUM(CASE WHEN discount_pico_usd > 0 THEN discount_pico_usd ELSE 0 END), 0)").As("saved_cost_pico_usd"),
			sqlite.Raw("COUNT(actual_cost_pico_usd)").As("requests_with_actual_cost"),
		),
		sm.From(bobmodels.RequestUsages.NameAsExpr()),
	)
	usageTotals, err := bob.One(ctx, r.exec, usageQuery, scan.StructMapper[usageSummaryAggregate]())
	if err != nil {
		return StatsSummary{}, err
	}
	summary := StatsSummary{
		TotalRequests: requestTotals.TotalRequests, SucceededRequests: requestTotals.SucceededRequests, FailedRequests: requestTotals.FailedRequests, PartialRequests: requestTotals.PartialRequests,
		TotalAttempts: requestTotals.TotalAttempts, RetriedRequests: requestTotals.RetriedRequests, RequestsWithTime: requestTotals.RequestsWithTime,
		EligibleRequests: requestTotals.EligibleRequests, ExcludedLimitRequests: requestTotals.ExcludedLimitRequests,
		InputTokens: usageTotals.InputTokens, OutputTokens: usageTotals.OutputTokens, TotalTokens: usageTotals.TotalTokens, CachedReadTokens: usageTotals.CachedReadTokens, CacheWriteTokens: usageTotals.CacheWriteTokens, ReasoningTokens: usageTotals.ReasoningTokens,
		EstimatedCostPico: usageTotals.EstimatedCostPico, OfficialCostPico: usageTotals.OfficialCostPico, ActualCostPico: usageTotals.ActualCostPico, SavedCostPico: usageTotals.SavedCostPico, RequestsWithActual: usageTotals.RequestsWithActual,
	}
	if requestTotals.FastestMS.Valid {
		value := requestTotals.FastestMS.Int64
		summary.FastestMS = &value
	}
	if requestTotals.SlowestMS.Valid {
		value := requestTotals.SlowestMS.Int64
		summary.SlowestMS = &value
	}
	if requestTotals.AverageMS.Valid {
		value := requestTotals.AverageMS.Int64
		summary.AverageMS = &value
	}
	if summary.OfficialCostPico > 0 {
		value := summary.SavedCostPico * 10000 / summary.OfficialCostPico
		summary.SavedPercentBPS = &value
	}
	if summary.EligibleRequests > 0 {
		summary.SuccessRateBPS = summary.SucceededRequests * 10000 / summary.EligibleRequests
	}
	return summary, nil
}

func (r *StatsRepository) ModelStats(ctx context.Context, freeModels map[string]bool) ([]ModelStats, error) {
	data, err := r.loadRequestData(ctx)
	if err != nil {
		return nil, err
	}
	type aggregate struct {
		item         ModelStats
		durations    []int64
		observedFree bool
	}
	byModel := make(map[string]*aggregate)
	for _, request := range data.requests {
		entry := byModel[request.LogicalModel]
		if entry == nil {
			entry = &aggregate{item: ModelStats{Model: request.LogicalModel}}
			byModel[request.LogicalModel] = entry
		}
		item := &entry.item
		item.Requests++
		if request.StatsDisposition == "included" {
			item.EligibleRequests++
		} else if request.StatsDisposition == "excluded_limit" {
			item.ExcludedLimitRequests++
		}
		switch request.State {
		case "succeeded":
			item.SucceededRequests++
		case "failed":
			item.FailedRequests++
		case "partial":
			item.PartialRequests++
		}
		item.TotalAttempts += request.AttemptCount
		if request.AttemptCount > 1 {
			item.RetriedRequests++
		}
		if request.DurationMS.Valid {
			item.RequestsWithTime++
			entry.durations = append(entry.durations, request.DurationMS.V)
		}
		for _, attempt := range data.attempts[request.ID] {
			if attempt.UpstreamModel.Valid && strings.HasSuffix(attempt.UpstreamModel.V, ":free") {
				entry.observedFree = true
			}
		}
		if usage := data.usage[request.ID]; usage != nil {
			item.InputTokens += usage.InputTokens
			item.OutputTokens += usage.OutputTokens
			item.TotalTokens += usage.TotalTokens
			item.CachedReadTokens += usage.CachedReadTokens
			item.CacheWriteTokens += usage.CacheWriteTokens
			item.ReasoningTokens += usage.ReasoningTokens
			item.EstimatedCostPico += usage.EstimatedCostPicoUsd
			item.OfficialCostPico += usage.OfficialCostPicoUsd
			if usage.ActualCostPicoUsd.Valid {
				item.ActualCostPico += usage.ActualCostPicoUsd.V
			}
			if usage.DiscountPicoUsd.Valid {
				item.DiscountPico += usage.DiscountPicoUsd.V
				if usage.DiscountPicoUsd.V > 0 {
					item.SavedCostPico += usage.DiscountPicoUsd.V
				}
			}
		}
	}
	result := make([]ModelStats, 0, len(byModel))
	for model, entry := range byModel {
		item := entry.item
		item.Free = freeModels[model] || entry.observedFree
		if item.Requests > 0 {
			if item.EligibleRequests > 0 {
				item.SuccessRateBPS = item.SucceededRequests * 10000 / item.EligibleRequests
			}
			item.RetryRateBPS = item.RetriedRequests * 10000 / item.Requests
		}
		if item.OfficialCostPico > 0 {
			value := item.DiscountPico * 10000 / item.OfficialCostPico
			item.DiscountBPS = &value
		}
		if len(entry.durations) > 0 {
			min, max, total := entry.durations[0], entry.durations[0], int64(0)
			for _, value := range entry.durations {
				if value < min {
					min = value
				}
				if value > max {
					max = value
				}
				total += value
			}
			average := total / int64(len(entry.durations))
			item.FastestMS, item.SlowestMS, item.AverageMS = &min, &max, &average
		}
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Requests == result[j].Requests {
			return result[i].Model < result[j].Model
		}
		return result[i].Requests > result[j].Requests
	})
	return result, nil
}

func (r *StatsRepository) ProviderStats(ctx context.Context) ([]ProviderStats, error) {
	data, err := r.loadRequestData(ctx)
	if err != nil {
		return nil, err
	}
	type aggregate struct {
		item      ProviderStats
		durations []int64
	}
	byProvider := make(map[string]*aggregate)
	for _, request := range data.requests {
		provider := "unknown"
		if request.SelectedProvider.Valid && request.SelectedProvider.V != "" {
			provider = request.SelectedProvider.V
		}
		entry := byProvider[provider]
		if entry == nil {
			entry = &aggregate{item: ProviderStats{Provider: provider}}
			byProvider[provider] = entry
		}
		item := &entry.item
		item.Requests++
		if request.StatsDisposition == "included" {
			item.EligibleRequests++
		} else if request.StatsDisposition == "excluded_limit" {
			item.ExcludedLimitRequests++
		}
		switch request.State {
		case "succeeded":
			item.SucceededRequests++
		case "failed":
			item.FailedRequests++
		case "partial":
			item.PartialRequests++
		}
		item.TotalAttempts += request.AttemptCount
		if request.AttemptCount > 1 {
			item.RetriedRequests++
		}
		if request.DurationMS.Valid {
			item.RequestsWithTime++
			entry.durations = append(entry.durations, request.DurationMS.V)
		}
		if usage := data.usage[request.ID]; usage != nil {
			item.InputTokens += usage.InputTokens
			item.OutputTokens += usage.OutputTokens
			item.TotalTokens += usage.TotalTokens
			item.CachedReadTokens += usage.CachedReadTokens
			item.CacheWriteTokens += usage.CacheWriteTokens
			item.ReasoningTokens += usage.ReasoningTokens
			item.EstimatedCostPico += usage.EstimatedCostPicoUsd
			item.OfficialCostPico += usage.OfficialCostPicoUsd
			if usage.ActualCostPicoUsd.Valid {
				item.ActualCostPico += usage.ActualCostPicoUsd.V
			}
			if usage.DiscountPicoUsd.Valid && usage.DiscountPicoUsd.V > 0 {
				item.SavedCostPico += usage.DiscountPicoUsd.V
			}
		}
	}
	result := make([]ProviderStats, 0, len(byProvider))
	for _, entry := range byProvider {
		item := entry.item
		if item.Requests > 0 {
			if item.EligibleRequests > 0 {
				item.SuccessRateBPS = item.SucceededRequests * 10000 / item.EligibleRequests
			}
			item.RetryRateBPS = item.RetriedRequests * 10000 / item.Requests
		}
		if item.OfficialCostPico > 0 {
			value := item.SavedCostPico * 10000 / item.OfficialCostPico
			if value < 0 {
				value = 0
			}
			if value > 10000 {
				value = 10000
			}
			item.DiscountBPS = &value
		}
		if len(entry.durations) > 0 {
			min, max, total := entry.durations[0], entry.durations[0], int64(0)
			for _, value := range entry.durations {
				if value < min {
					min = value
				}
				if value > max {
					max = value
				}
				total += value
			}
			average := total / int64(len(entry.durations))
			item.FastestMS, item.SlowestMS, item.AverageMS = &min, &max, &average
		}
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Requests == result[j].Requests {
			return result[i].Provider < result[j].Provider
		}
		return result[i].Requests > result[j].Requests
	})
	return result, nil
}

func requestStatFromBob(request *bobmodels.ProxyRequest, usage *bobmodels.RequestUsage) RequestStat {
	item := RequestStat{ID: request.ID, SessionID: stringPointer(request.SessionID), Protocol: request.Protocol, Model: request.LogicalModel, State: request.State, ReceivedAt: request.ReceivedAt, Attempts: request.AttemptCount}
	item.CompletedAt = stringPointer(request.CompletedAt)
	item.ErrorCode = stringPointer(request.ErrorCode)
	item.ErrorMessage = stringPointer(request.ErrorMessage)
	item.DurationMS = int64Pointer(request.DurationMS)
	item.Provider = stringValue(request.SelectedProvider)
	item.UpstreamModel = stringValue(request.SelectedUpstreamModel)
	if usage != nil {
		item.InputTokens, item.OutputTokens, item.TotalTokens = usage.InputTokens, usage.OutputTokens, usage.TotalTokens
		item.CachedReadTokens, item.CacheWriteTokens, item.ReasoningTokens = usage.CachedReadTokens, usage.CacheWriteTokens, usage.ReasoningTokens
		item.EstimatedCostPico = usage.EstimatedCostPicoUsd
		item.OfficialCostPico = pointerInt64(usage.OfficialCostPicoUsd)
		item.ActualCostPico = int64Pointer(usage.ActualCostPicoUsd)
		item.DiscountPico = int64Pointer(usage.DiscountPicoUsd)
		item.DiscountBPS = int64Pointer(usage.DiscountPercentBPS)
	}
	return item
}

func skippedRoutesFromPlan(plan sql.Null[string], providers map[string]string) []models.SkippedRouteStat {
	if !plan.Valid || strings.TrimSpace(plan.V) == "" {
		return nil
	}
	var stored struct {
		Rejections []struct {
			RouteID       string `json:"route_id"`
			Provider      string `json:"provider"`
			LogicalModel  string `json:"logical_model"`
			UpstreamModel string `json:"upstream_model"`
			Code          string `json:"code"`
			Detail        string `json:"detail"`
		} `json:"rejections"`
	}
	if err := json.Unmarshal([]byte(plan.V), &stored); err != nil {
		return nil
	}
	result := make([]models.SkippedRouteStat, 0)
	seen := make(map[string]bool)
	for _, rejection := range stored.Rejections {
		// wrong_model entries describe every other catalog route and are not
		// candidates for this request. All other rejection codes occur after
		// model matching and therefore identify a route considered for it.
		if rejection.Code == "wrong_model" || rejection.RouteID == "" || seen[rejection.RouteID] {
			continue
		}
		provider, upstream := rejection.Provider, rejection.UpstreamModel
		if provider == "" || upstream == "" {
			prefix, suffix, ok := strings.Cut(rejection.RouteID, ":")
			if ok {
				if provider == "" {
					provider = providers[prefix]
				}
				if upstream == "" {
					upstream = suffix
				}
			}
		}
		seen[rejection.RouteID] = true
		result = append(result, models.SkippedRouteStat{RouteID: rejection.RouteID, Provider: provider, UpstreamModel: upstream, State: "skipped", ReasonCode: rejection.Code, Reason: rejection.Detail})
	}
	return result
}

func attemptStatFromBob(attempt *bobmodels.ProxyAttempt) AttemptStat {
	return AttemptStat{Number: attempt.AttemptNumber, Provider: stringValue(attempt.Provider), UpstreamModel: stringValue(attempt.UpstreamModel), State: attempt.State, StartedAt: attempt.StartedAt, CompletedAt: stringValue(attempt.CompletedAt), DurationMS: int64Pointer(attempt.DurationMS), HTTPStatus: int64Pointer(attempt.HTTPStatus), ErrorClass: stringValue(attempt.ErrorClass), ErrorMessage: stringValue(attempt.ErrorMessage), RawError: stringValue(attempt.ErrorRaw), ClientFormat: stringValue(attempt.ClientFormat), ProviderFormat: stringValue(attempt.ProviderFormat)}
}
