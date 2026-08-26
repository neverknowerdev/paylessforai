package repositories

import (
	"context"
	"time"

	bobmodels "github.com/neverknowerdev/paylessforai/internal/db/bob/models"
)

type ProxyRequestsRepository struct{ bobRepository }

func (r *ProxyRequestsRepository) Create(ctx context.Context, id, clientKeyID, protocol, model string) error {
	state := "received"
	receivedAt := time.Now().UTC().Format(time.RFC3339Nano)
	client := nullableString(pointerIfNonEmpty(clientKeyID))
	setter := &bobmodels.ProxyRequestSetter{ID: &id, ClientKeyID: &client, Protocol: &protocol, LogicalModel: &model, State: &state, ReceivedAt: &receivedAt}
	_, err := bobmodels.ProxyRequests.Insert(setter).One(ctx, r.exec)
	return err
}

func (r *ProxyRequestsRepository) RecordAttemptRoute(ctx context.Context, requestID string, attempt int, provider, upstream string) error {
	row, err := bobmodels.FindProxyRequest(ctx, r.exec, requestID)
	if err != nil {
		return err
	}
	providerValue := nullableString(pointerIfNonEmpty(provider))
	upstreamValue := nullableString(pointerIfNonEmpty(upstream))
	attemptCount := int64(attempt)
	if row.AttemptCount > attemptCount {
		attemptCount = row.AttemptCount
	}
	return row.Update(ctx, r.exec, &bobmodels.ProxyRequestSetter{SelectedProvider: &providerValue, SelectedUpstreamModel: &upstreamValue, AttemptCount: &attemptCount})
}

func (r *ProxyRequestsRepository) Complete(ctx context.Context, id, state, code, message string) error {
	row, err := bobmodels.FindProxyRequest(ctx, r.exec, id)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	duration := int64(0)
	if parsed, parseErr := time.Parse(time.RFC3339Nano, row.ReceivedAt); parseErr == nil {
		duration = now.Sub(parsed).Milliseconds()
		if duration < 0 {
			duration = 0
		}
	}
	errorCode := nullableString(pointerIfNonEmpty(code))
	errorMessage := nullableString(pointerIfNonEmpty(message))
	durationValue := nullableInt64(&duration)
	setter := &bobmodels.ProxyRequestSetter{State: &state, CompletedAt: nullableStringPointer(pointerString(now.Format(time.RFC3339Nano))), DurationMS: &durationValue, ErrorCode: &errorCode, ErrorMessage: &errorMessage}
	if code == "provider_rate_limit" || code == "provider_quota_exhausted" || code == "all_subscription_quotas_exhausted" {
		disposition := "excluded_limit"
		setter.StatsDisposition = &disposition
	}
	return row.Update(ctx, r.exec, setter)
}

func pointerIfNonEmpty(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
