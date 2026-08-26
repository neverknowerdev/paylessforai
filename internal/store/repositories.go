package store

import (
	"context"
	"database/sql"
)

// Repositories groups one repository per persisted table. Repositories own
// SQL for their table; Store remains the compatibility facade used by the
// HTTP/runtime packages.
type Repositories struct {
	Settings            *SettingsRepository
	ProviderCredentials *ProviderCredentialsRepository
	ClientAPIKeys       *ClientAPIKeysRepository
	CatalogRefreshes    *CatalogRefreshesRepository
	Models              *ModelsRepository
	ModelRoutes         *ModelRoutesRepository
	ProviderHealth      *ProviderHealthRepository
	ProxyRequests       *ProxyRequestsRepository
	ProxyAttempts       *ProxyAttemptsRepository
	RequestUsage        *RequestUsageRepository
}

func newRepositories(db DBTX) *Repositories {
	return &Repositories{
		Settings:            &SettingsRepository{db: db},
		ProviderCredentials: &ProviderCredentialsRepository{db: db},
		ClientAPIKeys:       &ClientAPIKeysRepository{db: db},
		CatalogRefreshes:    &CatalogRefreshesRepository{db: db},
		Models:              &ModelsRepository{db: db},
		ModelRoutes:         &ModelRoutesRepository{db: db},
		ProviderHealth:      &ProviderHealthRepository{db: db},
		ProxyRequests:       &ProxyRequestsRepository{db: db},
		ProxyAttempts:       &ProxyAttemptsRepository{db: db},
		RequestUsage:        &RequestUsageRepository{db: db},
	}
}

type DBTX interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}
