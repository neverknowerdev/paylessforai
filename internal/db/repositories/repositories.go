package repositories

import (
	"context"
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
		result.Groups = &RoutingGroupsRepository{bobRepository: bobRepository{exec: bobExec}, database: bob.NewDB(result.database)}
		result.Stats = &StatsRepository{bobRepository: bobRepository{exec: bobExec}}
		result.Subscriptions = &SubscriptionRepository{bobRepository: bobRepository{exec: bobExec}}
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

// GetSetting and SetSetting keep the small settings-store contract used by
// subsystems such as the updater while persistence remains repository-owned.
func (r *Repositories) GetSetting(ctx context.Context, key string) (string, bool, error) {
	if r == nil || r.Settings == nil {
		return "", false, errors.New("settings repository unavailable")
	}
	return r.Settings.Get(ctx, key)
}

func (r *Repositories) SetSetting(ctx context.Context, key, value string) error {
	if r == nil || r.Settings == nil {
		return errors.New("settings repository unavailable")
	}
	return r.Settings.Set(ctx, key, value)
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
