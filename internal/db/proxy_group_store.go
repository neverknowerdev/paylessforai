package db

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/neverknowerdev/paylessforai/internal/routing"
)

func (s *Store) RecordResolution(ctx context.Context, requestID string, plan routing.Plan) error {
	data, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	var selected string
	if entry := plan.Selected(); entry != nil {
		selected = entry.Route.LogicalModel
	}
	return s.Repositories.ProxyRequests.RecordResolution(ctx, requestID, plan.GroupID, plan.GroupRevision, string(data), selected)
}

func (s *Store) RecordProxyAttemptRoute(ctx context.Context, requestID string, attempt int, routeID, credentialID, stageID, stagePath, provider, upstream, state, errorClass, errorMessage string, rawError ...string) error {
	if err := s.recordProxyAttempt(ctx, requestID, attempt, provider, upstream, state, errorClass, errorMessage, rawError...); err != nil {
		return err
	}
	rawPath := strings.TrimSpace(stagePath)
	var raw any
	if rawPath != "" {
		raw = rawPath
	}
	var credential any
	if credentialID != "" {
		credential = credentialID
	}
	var stage any
	if stageID != "" {
		stage = stageID
	}
	_, err := s.db.ExecContext(ctx, `UPDATE proxy_attempts SET route_id=?,credential_id=?,group_stage_id=?,group_stage_path=? WHERE request_id=? AND attempt_number=?`, nullableStringReason(routeID), credential, stage, raw, requestID, attempt)
	return err
}

func nullableStringReason(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}
