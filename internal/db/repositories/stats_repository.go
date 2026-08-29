package repositories

import (
	"context"
	"fmt"
	"sort"
	"strings"

	bobmodels "github.com/neverknowerdev/paylessforai/internal/db/bob/models"
	"github.com/neverknowerdev/paylessforai/internal/db/models"
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

type statsData struct {
	requests []*bobmodels.ProxyRequest
	usage    map[string]*bobmodels.RequestUsage
	attempts map[string][]*bobmodels.ProxyAttempt
}

func (r *StatsRepository) load(ctx context.Context) (statsData, error) {
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

func (r *StatsRepository) ListRequestStats(ctx context.Context, limit int) ([]RequestStat, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	data, err := r.load(ctx)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(data.requests, func(i, j int) bool {
		return data.requests[i].ReceivedAt > data.requests[j].ReceivedAt
	})
	if len(data.requests) > limit {
		data.requests = data.requests[:limit]
	}
	result := make([]RequestStat, 0, len(data.requests))
	for _, request := range data.requests {
		item := requestStatFromBob(request, data.usage[request.ID])
		for _, attempt := range data.attempts[request.ID] {
			item.AttemptDetails = append(item.AttemptDetails, attemptStatFromBob(attempt))
		}
		result = append(result, item)
	}
	return result, nil
}

func (r *StatsRepository) RequestStatsSummary(ctx context.Context) (StatsSummary, error) {
	data, err := r.load(ctx)
	if err != nil {
		return StatsSummary{}, err
	}
	var summary StatsSummary
	var fastest, slowest, durationTotal int64
	for _, request := range data.requests {
		summary.TotalRequests++
		switch request.State {
		case "succeeded":
			summary.SucceededRequests++
		case "failed":
			summary.FailedRequests++
		case "partial":
			summary.PartialRequests++
		}
		if request.StatsDisposition == "included" {
			summary.EligibleRequests++
		} else if request.StatsDisposition == "excluded_limit" {
			summary.ExcludedLimitRequests++
		}
		summary.TotalAttempts += request.AttemptCount
		if request.AttemptCount > 1 {
			summary.RetriedRequests++
		}
		if request.DurationMS.Valid {
			value := request.DurationMS.V
			if summary.RequestsWithTime == 0 || value < fastest {
				fastest = value
			}
			if summary.RequestsWithTime == 0 || value > slowest {
				slowest = value
			}
			durationTotal += value
			summary.RequestsWithTime++
		}
		if usage := data.usage[request.ID]; usage != nil {
			summary.InputTokens += usage.InputTokens
			summary.OutputTokens += usage.OutputTokens
			summary.TotalTokens += usage.TotalTokens
			summary.CachedReadTokens += usage.CachedReadTokens
			summary.CacheWriteTokens += usage.CacheWriteTokens
			summary.ReasoningTokens += usage.ReasoningTokens
			summary.EstimatedCostPico += usage.EstimatedCostPicoUsd
			summary.OfficialCostPico += usage.OfficialCostPicoUsd
			if usage.ActualCostPicoUsd.Valid {
				summary.ActualCostPico += usage.ActualCostPicoUsd.V
				summary.RequestsWithActual++
			}
			if usage.DiscountPicoUsd.Valid && usage.DiscountPicoUsd.V > 0 {
				summary.SavedCostPico += usage.DiscountPicoUsd.V
			}
		}
	}
	if summary.RequestsWithTime > 0 {
		summary.FastestMS = &fastest
		summary.SlowestMS = &slowest
		average := durationTotal / summary.RequestsWithTime
		summary.AverageMS = &average
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
	data, err := r.load(ctx)
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
	data, err := r.load(ctx)
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
	item := RequestStat{ID: request.ID, Protocol: request.Protocol, Model: request.LogicalModel, State: request.State, ReceivedAt: request.ReceivedAt, Attempts: request.AttemptCount}
	item.CompletedAt = stringPointer(request.CompletedAt)
	item.ErrorCode = stringPointer(request.ErrorCode)
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

func attemptStatFromBob(attempt *bobmodels.ProxyAttempt) AttemptStat {
	return AttemptStat{Number: attempt.AttemptNumber, Provider: stringValue(attempt.Provider), UpstreamModel: stringValue(attempt.UpstreamModel), State: attempt.State, StartedAt: attempt.StartedAt, CompletedAt: stringValue(attempt.CompletedAt), DurationMS: int64Pointer(attempt.DurationMS), ErrorClass: stringValue(attempt.ErrorClass), ErrorMessage: stringValue(attempt.ErrorMessage), RawError: stringValue(attempt.ErrorRaw)}
}
