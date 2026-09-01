package db

import "github.com/neverknowerdev/paylessforai/internal/db/models"

// Model aliases preserve the small public data vocabulary while persistence
// operations themselves live on repositories.Repositories.
type ClientKey = models.ClientKey
type RequestUsage = models.RequestUsage
type ProviderCredential = models.ProviderCredential
type AttemptStat = models.AttemptStat
type RequestStat = models.RequestStat
type StatsSummary = models.StatsSummary
type ModelStats = models.ModelStats
type ProviderStats = models.ProviderStats
type GroupStats = models.GroupStats
type CatalogRefresh = models.CatalogRefresh
type ModelRecord = models.ModelRecord
type ModelRouteRecord = models.ModelRouteRecord
type ProviderHealthRecord = models.ProviderHealthRecord
