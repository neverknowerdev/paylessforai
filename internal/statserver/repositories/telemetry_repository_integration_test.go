package repositories_test

import (
	"testing"
	"time"

	"github.com/neverknowerdev/paylessforai/internal/statserver/models"
)

func TestTelemetryRepositorySQL(t *testing.T) {
	f := newIntegrationFixture(t)
	now := time.Now().UTC()
	batch := models.TelemetryBatch{BatchID: "batch-1", Events: []models.TelemetryEvent{{EventID: "event-1", ModelName: "DeepSeek V4 Pro", Provider: "fixture", OccurredAt: now, TotalMS: 120, TTFTMS: 20, GenerationMS: 100, InputTokens: 10, OutputTokens: 5, CacheStatus: "hit", CacheTTLSeconds: 60, Success: true}}}
	accepted, duplicate, err := f.repos.Telemetry.Ingest(f.ctx, "installation-hash", batch)
	if err != nil || accepted != 1 || duplicate {
		t.Fatalf("accepted=%d duplicate=%v err=%v", accepted, duplicate, err)
	}
	accepted, duplicate, err = f.repos.Telemetry.Ingest(f.ctx, "installation-hash", batch)
	if err != nil || accepted != 0 || !duplicate {
		t.Fatalf("duplicate accepted=%d duplicate=%v err=%v", accepted, duplicate, err)
	}
	stats, err := f.repos.Telemetry.Statistics(f.ctx, "DeepSeek V4 Pro", "fixture")
	if err != nil || stats.SampleCount != 1 || stats.CacheHits != 1 || stats.SuccessCount != 1 {
		t.Fatalf("stats=%+v err=%v", stats, err)
	}
}
