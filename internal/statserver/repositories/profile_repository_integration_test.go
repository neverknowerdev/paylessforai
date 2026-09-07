package repositories_test

import (
	"testing"

	"github.com/neverknowerdev/paylessforai/internal/statserver/models"
)

func TestProfileRepositorySQL(t *testing.T) {
	f := newIntegrationFixture(t)
	modelID, err := f.repos.Catalog.UpsertRecord(f.ctx, "fixture", catalogRecord())
	if err != nil {
		t.Fatal(err)
	}
	public, err := f.repos.Profiles.Public(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(public) != 0 {
		t.Fatalf("unexpected profiles=%v", public)
	}
	profileID, versionID, err := f.repos.Profiles.Create(f.ctx, models.CreateProfile{Key: "quality", DisplayName: "Quality", Description: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if profileID == 0 || versionID == 0 {
		t.Fatal("missing created ids")
	}
	if err := f.repos.Profiles.CreateSignal(f.ctx, models.CreateSignal{Key: "manual", DisplayName: "Manual"}); err != nil {
		t.Fatal(err)
	}
	if err := f.repos.Profiles.AddComponent(f.ctx, versionID, models.CreateComponent{SignalType: "benchmark", Selector: "MMLU Pro", Weight: 2, MinValue: 0, MaxValue: 1, Direction: "higher"}); err != nil {
		t.Fatal(err)
	}
	components, err := f.repos.Profiles.Components(f.ctx, versionID)
	if err != nil || len(components) != 1 {
		t.Fatalf("components=%v err=%v", components, err)
	}
	if err := f.repos.Profiles.Publish(f.ctx, versionID); err != nil {
		t.Fatal(err)
	}
	admin, err := f.repos.Profiles.Admin(f.ctx)
	if err != nil || len(admin) != 1 {
		t.Fatalf("admin=%v err=%v", admin, err)
	}
	versions, err := f.repos.Profiles.PublishedVersions(f.ctx)
	if err != nil || len(versions) != 1 || len(versions[0].Components) != 1 {
		t.Fatalf("versions=%v err=%v", versions, err)
	}
	if err := f.repos.Profiles.UpsertScore(f.ctx, versionID, modelID, 82, .82, 1, models.ScoreComponent{Selector: "MMLU Pro", Value: .82, Weight: 2}); err != nil {
		t.Fatal(err)
	}
	scores, err := f.repos.Profiles.ScoresForModel(f.ctx, modelID)
	if err != nil || len(scores) != 1 || scores[0].Score != 82 {
		t.Fatalf("scores=%v err=%v", scores, err)
	}
}
