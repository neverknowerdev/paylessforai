package providers

import (
	"context"
	"fmt"
	"net/http"

	"github.com/neverknowerdev/paylessforai/internal/matcher"
)

// WithManualModels keeps user-verified model definitions available when an
// upstream has no catalog endpoint. Native discovery is still preferred and
// manual definitions are merged without replacing it.
type WithManualModels struct {
	Client Client
	Models []ManualModel
}

func (c WithManualModels) Name() string { return c.Client.Name() }

func (c WithManualModels) Do(ctx context.Context, protocol matcher.Protocol, model string, body []byte) (*http.Response, error) {
	return c.Client.Do(ctx, protocol, model, body)
}

func (c WithManualModels) Discover(ctx context.Context) ([]Model, error) {
	models, err := c.Client.Discover(ctx)
	if err != nil && len(c.Models) == 0 {
		return nil, err
	}
	byID := make(map[string]bool, len(models))
	for _, model := range models {
		byID[model.ID] = true
	}
	manual := make([]Model, 0, len(c.Models))
	for _, specification := range c.Models {
		if specification.ID == "" || byID[specification.ID] {
			continue
		}
		price := matcher.Price{InputPicoUSDPerToken: specification.InputPicoUSDPerToken, OutputPicoUSDPerToken: specification.OutputPicoUSDPerToken}
		manual = append(manual, Model{ID: specification.ID, Name: specification.ID, ContextLength: specification.ContextLength, MaxCompletionTokens: specification.MaxCompletionTokens, Pricing: price, PriceAvailable: specification.InputPicoUSDPerToken > 0 && specification.OutputPicoUSDPerToken > 0, OfficialPricing: price, InputModalities: normalizeTags(specification.InputModalities), OutputModalities: normalizeTags(specification.OutputModalities), Tags: normalizeTags(specification.Tags)})
	}
	models = append(models, manual...)
	if len(models) == 0 {
		return nil, fmt.Errorf("provider returned no models")
	}
	return models, nil
}
