package repositories

import (
	"context"
	"time"

	bobmodels "github.com/neverknowerdev/paylessforai/internal/db/bob/models"
)

type ProxyRequestsRepository struct{ bobRepository }

func (r *ProxyRequestsRepository) Create(ctx context.Context, id, clientKeyID, protocol, model string, sessionIDs ...string) error {
	state := "received"
	receivedAt := time.Now().UTC().Format(time.RFC3339Nano)
	client := nullableString(pointerIfNonEmpty(clientKeyID))
	sessionID := ""
	if len(sessionIDs) > 0 {
		sessionID = sessionIDs[0]
	}
	session := nullableString(pointerIfNonEmpty(sessionID))
	setter := &bobmodels.ProxyRequestSetter{ID: &id, ClientKeyID: &client, SessionID: &session, Protocol: &protocol, LogicalModel: &model, State: &state, ReceivedAt: &receivedAt}
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

func (r *ProxyRequestsRepository) RecordResolution(ctx context.Context, requestID, groupID string, groupRevision int64, planJSON, selectedModel string) error {
	row, err := bobmodels.FindProxyRequest(ctx, r.exec, requestID)
	if err != nil {
		return err
	}
	var revision *int64
	if groupRevision != 0 {
		revision = &groupRevision
	}
	revisionValue := nullableInt64(revision)
	return row.Update(ctx, r.exec, &bobmodels.ProxyRequestSetter{
		ResolvedGroupID:       nullableStringPointer(pointerIfNonEmpty(groupID)),
		ResolvedGroupRevision: &revisionValue,
		ResolvedPlanJSON:      nullableStringPointer(pointerIfNonEmpty(planJSON)),
		SelectedLogicalModel:  nullableStringPointer(pointerIfNonEmpty(selectedModel)),
	})
}

func (r *ProxyRequestsRepository) Complete(ctx context.Context, id, state, code, message string) error {
	return r.CompleteWithDisposition(ctx, id, state, code, message, code)
}

// CompleteWithDisposition keeps the client-facing error code separate from
// the internal failure classification used for subscription accounting. A
// request can therefore report all_provider_attempts_failed while still being
// excluded from usage totals when its final failure was a quota/rate limit.
func (r *ProxyRequestsRepository) CompleteWithDisposition(ctx context.Context, id, state, code, message, classification string) error {
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
	if classification == "provider_rate_limit" || classification == "provider_quota_exhausted" || classification == "all_subscription_quotas_exhausted" {
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
