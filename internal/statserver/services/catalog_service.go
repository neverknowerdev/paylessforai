package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/neverknowerdev/paylessforai/internal/statserver/connectors"
	"github.com/neverknowerdev/paylessforai/internal/statserver/models"
	"github.com/neverknowerdev/paylessforai/internal/statserver/repositories"
)

type CatalogService struct {
	repositories *repositories.Repositories
	connectors   []connectors.Connector
}

func NewCatalog(repos *repositories.Repositories, sources []connectors.Connector) *CatalogService {
	return &CatalogService{repositories: repos, connectors: sources}
}

func (s *CatalogService) Refresh(ctx context.Context) error {
	var errors []error
	for _, connector := range s.connectors {
		if connector.Key == "" && connector.Name != "huggingface" && connector.Name != "surplus" {
			continue
		}
		if err := s.refreshSource(ctx, connector); err != nil {
			errors = append(errors, fmt.Errorf("%s: %w", connector.Name, err))
		}
	}
	return joinErrors(errors)
}

func (s *CatalogService) refreshSource(ctx context.Context, connector connectors.Connector) error {
	source := models.Source{Key: connector.Name, DisplayName: connector.DisplayName, BaseURL: connector.URL}
	sourceID, err := s.repositories.Sources.StartRefresh(ctx, source)
	if err != nil {
		return err
	}
	records, err := connector.Fetch(ctx, connector.Key)
	if err != nil {
		_ = s.repositories.Sources.RecordFailure(ctx, sourceID, err)
		return err
	}
	payload, err := json.Marshal(records)
	if err != nil {
		return fmt.Errorf("encode source snapshot: %w", err)
	}
	hash := sha256.Sum256(payload)
	if err := s.repositories.Sources.RecordSnapshot(ctx, sourceID, hex.EncodeToString(hash[:]), payload); err != nil {
		return err
	}
	for _, record := range records {
		if _, err := s.repositories.Catalog.UpsertRecord(ctx, connector.Name, record); err != nil {
			return err
		}
	}
	return s.repositories.Sources.RecordSuccess(ctx, sourceID, len(records))
}

func (s *CatalogService) Ready(ctx context.Context) (bool, error) {
	count, err := s.repositories.Catalog.Count(ctx)
	return count > 0, err
}
func (s *CatalogService) List(ctx context.Context, limit int) ([]models.ModelSummary, int, error) {
	return s.repositories.Catalog.List(ctx, limit)
}
func (s *CatalogService) Search(ctx context.Context, query string) ([]models.SearchResult, error) {
	return s.repositories.Catalog.Search(ctx, models.Normalize(query))
}
func (s *CatalogService) Resolve(ctx context.Context, name string) (models.ModelSummary, error) {
	return s.repositories.Catalog.Resolve(ctx, models.Normalize(name))
}
func (s *CatalogService) Detail(ctx context.Context, slug string) (models.ModelDetail, error) {
	return s.repositories.Catalog.Detail(ctx, slug)
}
func (s *CatalogService) Sources(ctx context.Context) ([]models.Source, error) {
	return s.repositories.Sources.List(ctx)
}
func (s *CatalogService) Pricing(ctx context.Context, query string, limit, offset int) ([]models.PricingRow, int, error) {
	return s.repositories.Catalog.ListPricing(ctx, query, limit, offset)
}
func (s *CatalogService) OverridePricing(ctx context.Context, offeringID, userID int64, override models.PriceOverride) error {
	if err := validatePriceOverride(override); err != nil {
		return err
	}
	return s.repositories.Catalog.UpdatePriceOverride(ctx, offeringID, userID, override)
}
