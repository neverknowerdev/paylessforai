package repositories

import (
	"context"
	"database/sql"
	"strings"

	"github.com/neverknowerdev/paylessforai/internal/db/models"
	"github.com/stephenafamo/bob"
)

// Repositories groups one repository per persisted table. Repositories own
// SQL for their table; Store exposes the application-facing database facade.
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

func New(db DBTX) *Repositories {
	var bobExec bob.Executor
	if provider, ok := db.(interface{ BobExecutor() bob.Executor }); ok {
		bobExec = provider.BobExecutor()
	}
	return &Repositories{
		Settings:            &SettingsRepository{db: db},
		ProviderCredentials: &ProviderCredentialsRepository{db: db},
		ClientAPIKeys:       &ClientAPIKeysRepository{db: db},
		CatalogRefreshes:    &CatalogRefreshesRepository{db: db},
		Models:              &ModelsRepository{db: db, bob: bobExec},
		ModelRoutes:         &ModelRoutesRepository{db: db},
		ProviderHealth:      &ProviderHealthRepository{db: db},
		ProxyRequests:       &ProxyRequestsRepository{db: db},
		ProxyAttempts:       &ProxyAttemptsRepository{db: db},
		RequestUsage:        &RequestUsageRepository{db: db},
	}
}

// Model aliases keep repository signatures readable while model ownership
// remains in the db/models package.
type ClientKey = models.ClientKey
type ProviderCredential = models.ProviderCredential
type RequestUsage = models.RequestUsage
type CatalogRefresh = models.CatalogRefresh
type ModelRecord = models.ModelRecord
type ModelRouteRecord = models.ModelRouteRecord
type ProviderHealthRecord = models.ProviderHealthRecord

type DBTX interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func NULLString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}
