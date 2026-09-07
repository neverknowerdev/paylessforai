package services

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/neverknowerdev/paylessforai/internal/statserver/models"
)

func TestTelemetryValidationDoesNotReachStorage(t *testing.T) {
	service := NewTelemetry(nil)
	if _, _, err := service.Ingest(context.Background(), "short", models.TelemetryBatch{}); err != ErrInvalidCredential {
		t.Fatalf("credential err=%v", err)
	}
	if _, _, err := service.Ingest(context.Background(), strings.Repeat("x", 16), models.TelemetryBatch{}); err != ErrInvalidBatch {
		t.Fatalf("batch err=%v", err)
	}
}

func TestAuthPasswordHelpers(t *testing.T) {
	hash := hashPassword("secret")
	if !constantEqual(hash, hashPassword("secret")) {
		t.Fatal("equal hashes did not compare")
	}
	if constantEqual(hash, hashPassword("other")) {
		t.Fatal("different hashes compared equal")
	}
	if len(randomToken()) != 64 {
		t.Fatal("unexpected token length")
	}
}

func TestJoinErrors(t *testing.T) {
	if joinErrors(nil) != nil {
		t.Fatal("nil errors should remain nil")
	}
	if joinErrors([]error{errors.New("a"), errors.New("b")}) == nil {
		t.Fatal("joined errors should be non-nil")
	}
}

func TestPriceOverrideValidation(t *testing.T) {
	negative := -1.0
	if err := validatePriceOverride(models.PriceOverride{InputUSDPerMillion: &negative}); !errors.Is(err, ErrInvalidPriceOverride) {
		t.Fatalf("negative price err=%v", err)
	}
	valid := 1.25
	if err := validatePriceOverride(models.PriceOverride{InputUSDPerMillion: &valid}); err != nil {
		t.Fatalf("valid price err=%v", err)
	}
}
