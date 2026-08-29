package repositories

import (
	"database/sql"
	"errors"
	"github.com/neverknowerdev/paylessforai/internal/db/models"
	"github.com/stephenafamo/bob"
)

// Repositories groups the application-facing repositories for all persisted
// data. It also owns the connection opened by db.Open so callers never need a
// database facade with duplicated methods.
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
	Groups              *RoutingGroupsRepository
	Stats               *StatsRepository
	Subscriptions       *SubscriptionRepository
	database            *sql.DB
}

type DBTX interface {
	BobExecutor() bob.Executor
}

type SQLDBTX interface {
	SQLDB() *sql.DB
}

func New(db DBTX) *Repositories {
	var bobExec bob.Executor
	if provider, ok := db.(interface{ BobExecutor() bob.Executor }); ok {
		bobExec = provider.BobExecutor()
	}
	if bobExec == nil {
		panic("repositories.New requires a Bob executor")
	}
	result := &Repositories{
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
	if provider, ok := db.(SQLDBTX); ok {
		result.database = provider.SQLDB()
		result.Groups = &RoutingGroupsRepository{database: result.database}
		result.Stats = &StatsRepository{database: result.database}
		result.Subscriptions = &SubscriptionRepository{database: result.database}
	}
	return result
}

func (r *Repositories) DB() *sql.DB {
	if r == nil {
		return nil
	}
	return r.database
}

func (r *Repositories) Ping() error {
	if r == nil || r.database == nil {
		return errors.New("database unavailable")
	}
	return r.database.Ping()
}

func (r *Repositories) Close() error {
	if r == nil || r.database == nil {
		return nil
	}
	return r.database.Close()
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
