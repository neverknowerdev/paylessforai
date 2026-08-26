package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	bobmodels "github.com/neverknowerdev/paylessforai/internal/db/bob/models"
	"github.com/neverknowerdev/paylessforai/internal/retry"
	"github.com/stephenafamo/bob"
	"github.com/stephenafamo/bob/dialect/sqlite/dialect"
	"github.com/stephenafamo/bob/dialect/sqlite/im"
)

type ProxyAttemptsRepository struct{ bobRepository }

func (r *ProxyAttemptsRepository) Record(ctx context.Context, requestID string, attempt int, provider, upstream, state, errorClass, errorMessage string, rawError ...string) error {
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
	disposition := "included"
	if errorClass == string(retry.ErrorRateLimit) || errorClass == string(retry.ErrorQuotaExhausted) {
		disposition = "excluded_limit"
	}
	durationValue := nullableInt64(duration)
	deliveryState := "nothing_sent"
	setter := &bobmodels.ProxyAttemptSetter{ID: &id, RequestID: &requestID, AttemptNumber: pointerInt64(int64(attempt)), RouteID: nullableStringPointer(routeID), Provider: nullableStringPointer(providerValue), UpstreamModel: nullableStringPointer(upstreamValue), State: &state, StartedAt: &startedAt, CompletedAt: nullableStringPointer(completedAt), DurationMS: &durationValue, ErrorClass: nullableStringPointer(errorClassValue), ErrorMessage: nullableStringPointer(errorMessageValue), ErrorRaw: nullableStringPointer(rawValue), DeliveryState: &deliveryState, StatsDisposition: &disposition}
	_, err = bobmodels.ProxyAttempts.Insert(setter, upsertAttemptFields()).One(ctx, r.exec)
	return err
}

func upsertAttemptFields() bob.Mod[*dialect.InsertQuery] {
	return im.OnConflict("id").DoUpdate(im.SetExcluded("provider", "upstream_model", "state", "completed_at", "duration_ms", "error_class", "error_message", "error_raw", "stats_disposition"))
}
