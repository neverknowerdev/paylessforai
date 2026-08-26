package providers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/neverknowerdev/paylessforai/internal/matcher"
)

// VerifyModels verifies manually supplied model IDs before they are persisted
// as provider routes.
func (c *HTTPClient) VerifyModels(ctx context.Context, manual []ManualModel) ([]Model, error) {
	if len(manual) == 0 {
		return nil, fmt.Errorf("at least one model is required")
	}
	verified := make([]Model, 0, len(manual))
	for _, specification := range manual {
		id := strings.TrimSpace(specification.ID)
		if id == "" {
			return nil, fmt.Errorf("model ID is required")
		}
		if specification.InputPicoUSDPerToken <= 0 || specification.OutputPicoUSDPerToken <= 0 {
			return nil, fmt.Errorf("model %q needs positive input and output pricing", id)
		}
		body := []byte(`{"model":"manual-verification","messages":[{"role":"user","content":"Reply with OK"}],"max_tokens":1}`)
		response, err := c.Do(ctx, matcher.ProtocolChatCompletions, id, body)
		if err != nil {
			return nil, fmt.Errorf("model %q verification failed: %w", id, err)
		}
		response.Body.Close()
		price := matcher.Price{InputPicoUSDPerToken: specification.InputPicoUSDPerToken, OutputPicoUSDPerToken: specification.OutputPicoUSDPerToken, ObservedAt: time.Now().UTC()}
		verified = append(verified, Model{ID: id, Name: id, ContextLength: specification.ContextLength, MaxCompletionTokens: specification.MaxCompletionTokens, Pricing: price, PriceAvailable: specification.InputPicoUSDPerToken > 0 && specification.OutputPicoUSDPerToken > 0, OfficialPricing: price, OfficialPriceAvailable: false, SupportedParameters: []string{}, InputModalities: normalizeTags(specification.InputModalities), OutputModalities: normalizeTags(specification.OutputModalities), Tags: normalizeTags(specification.Tags)})
	}
	return verified, nil
}
