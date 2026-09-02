package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"github.com/neverknowerdev/paylessforai/internal/statserver/models"
	"github.com/neverknowerdev/paylessforai/internal/statserver/repositories"
)

var ErrInvalidCredential = errors.New("installation credential required")
var ErrInvalidBatch = errors.New("invalid telemetry batch")

type TelemetryService struct{ repositories *repositories.Repositories }

func NewTelemetry(repos *repositories.Repositories) *TelemetryService {
	return &TelemetryService{repositories: repos}
}

func (s *TelemetryService) Ingest(ctx context.Context, credential string, batch models.TelemetryBatch) (int, bool, error) {
	if len(credential) < 16 {
		return 0, false, ErrInvalidCredential
	}
	if batch.BatchID == "" || len(batch.Events) > 1000 {
		return 0, false, ErrInvalidBatch
	}
	hash := sha256.Sum256([]byte(credential))
	return s.repositories.Telemetry.Ingest(ctx, hex.EncodeToString(hash[:]), batch)
}
func (s *TelemetryService) Statistics(ctx context.Context, model, provider string) (models.Statistics, error) {
	return s.repositories.Telemetry.Statistics(ctx, model, provider)
}
