package repositories

import (
	"github.com/neverknowerdev/paylessforai/internal/db/models"
	"github.com/stephenafamo/bob"
)

// Repositories groups one Bob-backed repository per persisted table. Store
// exposes the application-facing database facade.
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

type DBTX interface {
	BobExecutor() bob.Executor
}

func New(db DBTX) *Repositories {
	var bobExec bob.Executor
	if provider, ok := db.(interface{ BobExecutor() bob.Executor }); ok {
		bobExec = provider.BobExecutor()
	}
	if bobExec == nil {
		panic("repositories.New requires a Bob executor")
	}
	return &Repositories{
		Settings:            &SettingsRepository{bobRepository: bobRepository{exec: bobExec}},
		ProviderCredentials: &ProviderCredentialsRepository{bobRepository: bobRepository{exec: bobExec}},
		ClientAPIKeys:       &ClientAPIKeysRepository{bobRepository: bobRepository{exec: bobExec}},
		CatalogRefreshes:    &CatalogRefreshesRepository{bobRepository: bobRepository{exec: bobExec}},
		Models:              &ModelsRepository{bobRepository: bobRepository{exec: bobExec}},
		ModelRoutes:         &ModelRoutesRepository{bobRepository: bobRepository{exec: bobExec}},
		ProviderHealth:      &ProviderHealthRepository{bobRepository: bobRepository{exec: bobExec}},
		ProxyRequests:       &ProxyRequestsRepository{bobRepository: bobRepository{exec: bobExec}},
		ProxyAttempts:       &ProxyAttemptsRepository{bobRepository: bobRepository{exec: bobExec}},
		RequestUsage:        &RequestUsageRepository{bobRepository: bobRepository{exec: bobExec}},
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

func boolInt(value bool) int64 {
	if value {
		return 1
	}
	return 0
}
