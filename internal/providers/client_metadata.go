package providers

import "github.com/neverknowerdev/paylessforai/internal/matcher"

// ClientMetadata is optional provider-client identity used by the catalog.
// Plain clients remain compatible and default to one metered route per provider.
type ClientMetadata interface {
	ExecutionKey() string
	CredentialID() string
	BillingClass() matcher.BillingClass
}
