package services

import (
	"context"
	"errors"

	"github.com/neverknowerdev/paylessforai/internal/statserver/models"
	"github.com/neverknowerdev/paylessforai/internal/statserver/repositories"
)

var ErrInvalidProfile = errors.New("invalid capability profile")
var ErrInvalidComponent = errors.New("invalid capability profile component")

type ProfileService struct{ repositories *repositories.Repositories }

func NewProfiles(repos *repositories.Repositories) *ProfileService {
	return &ProfileService{repositories: repos}
}
func (s *ProfileService) Public(ctx context.Context) ([]models.Profile, error) {
	return s.repositories.Profiles.Public(ctx)
}
func (s *ProfileService) Admin(ctx context.Context) ([]models.Profile, error) {
	return s.repositories.Profiles.Admin(ctx)
}
func (s *ProfileService) Scores(ctx context.Context, slug string) ([]models.CapabilityScore, error) {
	id, err := s.repositories.Catalog.ModelID(ctx, slug)
	if err != nil {
		return nil, err
	}
	return s.repositories.Profiles.ScoresForModel(ctx, id)
}
func (s *ProfileService) Create(ctx context.Context, input models.CreateProfile) (int64, int64, error) {
	if input.Key == "" || input.DisplayName == "" {
		return 0, 0, ErrInvalidProfile
	}
	return s.repositories.Profiles.Create(ctx, input)
}
func (s *ProfileService) CreateSignal(ctx context.Context, input models.CreateSignal) error {
	if input.Key == "" {
		return ErrInvalidProfile
	}
	return s.repositories.Profiles.CreateSignal(ctx, input)
}
func (s *ProfileService) AddComponent(ctx context.Context, versionID int64, input models.CreateComponent) error {
	if input.SignalType == "" || input.Weight <= 0 {
		return ErrInvalidComponent
	}
	if input.MaxValue == 0 && input.MinValue == 0 {
		input.MaxValue = 1
	}
	if input.Direction == "" {
		input.Direction = "higher"
	}
	if err := s.repositories.Profiles.AddComponent(ctx, versionID, input); err != nil {
		return err
	}
	return s.Compute(ctx)
}
func (s *ProfileService) Publish(ctx context.Context, versionID int64) error {
	if err := s.repositories.Profiles.Publish(ctx, versionID); err != nil {
		return err
	}
	return s.Compute(ctx)
}

func (s *ProfileService) Compute(ctx context.Context) error {
	versions, err := s.repositories.Profiles.PublishedVersions(ctx)
	if err != nil {
		return err
	}
	modelIDs, err := s.repositories.Catalog.ModelIDs(ctx)
	if err != nil {
		return err
	}
	for _, version := range versions {
		if len(version.Components) == 0 {
			continue
		}
		for _, modelID := range modelIDs {
			parts := []models.ScoreComponent{}
			for _, component := range version.Components {
				if component.SignalType != "benchmark" || component.MaxValue <= component.MinValue {
					continue
				}
				value, err := s.repositories.Catalog.LatestBenchmark(ctx, modelID, component.Selector)
				if err != nil {
					continue
				}
				normalized := (value - component.MinValue) / (component.MaxValue - component.MinValue)
				if component.Direction == "lower" {
					normalized = 1 - normalized
				}
				if normalized < 0 {
					normalized = 0
				}
				if normalized > 1 {
					normalized = 1
				}
				parts = append(parts, models.ScoreComponent{Selector: component.Selector, Value: normalized, Weight: component.Weight})
			}
			if len(parts) == 0 {
				continue
			}
			var weight, weighted float64
			for _, part := range parts {
				weight += float64(part.Weight)
				weighted += float64(part.Weight) * part.Value
			}
			base := weighted / weight
			if err := s.repositories.Profiles.UpsertScore(ctx, version.ID, modelID, 100*base, base, 1, map[string]any{"components": parts, "policy": version.MissingDataPolicy}); err != nil {
				return err
			}
		}
	}
	return nil
}
