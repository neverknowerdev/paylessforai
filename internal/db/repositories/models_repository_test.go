package repositories_test

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/neverknowerdev/paylessforai/internal/db/models"
)

func TestModelsRepositoryIntegration(t *testing.T) {
	i := newIntegrationDB(t)
	i.reset(t)
	model := models.ModelRecord{ID: "model-1", DisplayName: "Model", ContextLength: 1000, MaxOutputTokens: 500, MetadataJSON: "{}", ObservedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if err := i.repos.Models.Upsert(i.ctx, model); err != nil {
		t.Fatal(err)
	}
	if got, err := i.repos.Models.Get(i.ctx, model.ID); err != nil || got.DisplayName != model.DisplayName {
		t.Fatalf("get: %+v, %v", got, err)
	}
	if err := i.repos.Models.DeleteAll(i.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := i.repos.Models.Get(i.ctx, model.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted model error: %v", err)
	}
}
