package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	bobmodels "github.com/neverknowerdev/paylessforai/internal/db/bob/models"
	"github.com/neverknowerdev/paylessforai/internal/retry"
	"github.com/neverknowerdev/paylessforai/internal/wire"
	"github.com/stephenafamo/bob"
	"github.com/stephenafamo/bob/dialect/sqlite/dialect"
	"github.com/stephenafamo/bob/dialect/sqlite/im"
)

type ProxyAttemptsRepository struct{ bobRepository }

func (r *ProxyAttemptsRepository) UpdateRoute(ctx context.Context, requestID string, attempt int, routeID, credentialID, stageID, stagePath string) error {
	id := fmt.Sprintf("%s:%d", requestID, attempt)
	row, err := bobmodels.FindProxyAttempt(ctx, r.exec, id)
	if err != nil {
		return err
	}
	return row.Update(ctx, r.exec, &bobmodels.ProxyAttemptSetter{
		RouteID:        nullableStringPointer(pointerIfNonEmpty(routeID)),
		CredentialID:   nullableStringPointer(pointerIfNonEmpty(credentialID)),
		GroupStageID:   nullableStringPointer(pointerIfNonEmpty(stageID)),
		GroupStagePath: nullableStringPointer(pointerIfNonEmpty(stagePath)),
	})
}

func (r *ProxyAttemptsRepository) Record(ctx context.Context, requestID string, attempt int, provider, upstream, state, errorClass, errorMessage string, rawError ...string) error {
	return r.RecordWithHTTPStatus(ctx, requestID, attempt, provider, upstream, state, errorClass, errorMessage, wire.FormatUnknown, wire.FormatUnknown, nil, rawError...)
}

// RecordWithHTTPStatus persists the status and wire formats returned by an
// upstream when they exist. Transport and local routing failures intentionally
// keep the status nil.
func (r *ProxyAttemptsRepository) RecordWithHTTPStatus(ctx context.Context, requestID string, attempt int, provider, upstream, state, errorClass, errorMessage string, clientFormat, providerFormat wire.Format, httpStatus *int, rawError ...string) error {
	if attempt < 1 {
		return fmt.Errorf("attempt number must be positive")
	}
	id := fmt.Sprintf("%s:%d", requestID, attempt)
	now := time.Now().UTC()
	existing, err := bobmodels.FindProxyAttempt(ctx, r.exec, id)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	startedAt := now.Format(time.RFC3339Nano)
	if existing != nil {
		startedAt = existing.StartedAt
	}
	completedAt := (*string)(nil)
	duration := (*int64)(nil)
	if state != "started" {
		finished := now.Format(time.RFC3339Nano)
		completedAt = &finished
		elapsed := int64(0)
		if parsed, parseErr := time.Parse(time.RFC3339Nano, startedAt); parseErr == nil {
			elapsed = now.Sub(parsed).Milliseconds()
		}
		duration = &elapsed
	}
	routeID := pointerIfNonEmpty(provider + ":" + upstream)
	providerValue := pointerIfNonEmpty(provider)
	upstreamValue := pointerIfNonEmpty(upstream)
	errorClassValue := pointerIfNonEmpty(errorClass)
	errorMessageValue := pointerIfNonEmpty(errorMessage)
	raw := ""
	if len(rawError) > 0 {
		raw = rawError[0]
	}
	rawValue := pointerIfNonEmpty(raw)
	clientFormatValue := nullableStringPointer(pointerIfNonEmpty(string(clientFormat)))
	providerFormatValue := nullableStringPointer(pointerIfNonEmpty(string(providerFormat)))
	disposition := "included"
	if errorClass == string(retry.ErrorRateLimit) || errorClass == string(retry.ErrorQuotaExhausted) {
		disposition = "excluded_limit"
	}
	durationValue := nullableInt64(duration)
	statusValue := (*int64)(nil)
	if httpStatus != nil && *httpStatus >= 100 && *httpStatus <= 599 {
		value := int64(*httpStatus)
		statusValue = &value
	}
	httpStatusValue := nullableInt64(statusValue)
	deliveryState := "nothing_sent"
	setter := &bobmodels.ProxyAttemptSetter{ID: &id, RequestID: &requestID, AttemptNumber: pointerInt64(int64(attempt)), RouteID: nullableStringPointer(routeID), Provider: nullableStringPointer(providerValue), UpstreamModel: nullableStringPointer(upstreamValue), State: &state, StartedAt: &startedAt, CompletedAt: nullableStringPointer(completedAt), HTTPStatus: &httpStatusValue, DurationMS: &durationValue, ErrorClass: nullableStringPointer(errorClassValue), ErrorMessage: nullableStringPointer(errorMessageValue), ErrorRaw: nullableStringPointer(rawValue), DeliveryState: &deliveryState, StatsDisposition: &disposition, ClientFormat: clientFormatValue, ProviderFormat: providerFormatValue}
	_, err = bobmodels.ProxyAttempts.Insert(setter, upsertAttemptFields()).One(ctx, r.exec)
	return err
}

func upsertAttemptFields() bob.Mod[*dialect.InsertQuery] {
	return im.OnConflict("id").DoUpdate(im.SetExcluded("provider", "upstream_model", "state", "completed_at", "http_status", "duration_ms", "error_class", "error_message", "error_raw", "stats_disposition", "client_format", "provider_format"))
}
