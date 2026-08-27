package services

import (
	"errors"
	"math"

	"github.com/neverknowerdev/paylessforai/internal/statserver/models"
)

var ErrInvalidPriceOverride = errors.New("price override values must be finite and non-negative")

func validatePriceOverride(override models.PriceOverride) error {
	values := []*float64{override.InputUSDPerMillion, override.OutputUSDPerMillion, override.CacheReadUSDPerMillion, override.CacheWriteUSDPerMillion}
	for _, value := range values {
		if value != nil && (*value < 0 || math.IsNaN(*value) || math.IsInf(*value, 0)) {
			return ErrInvalidPriceOverride
		}
	}
	return nil
}

func joinErrors(errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}
