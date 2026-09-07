// Package repositories is the only layer that knows stat-server SQL.
package repositories

import "database/sql"

type Repositories struct {
	Catalog   *CatalogRepository
	Sources   *SourceRepository
	Telemetry *TelemetryRepository
	Profiles  *ProfileRepository
	Users     *UserRepository
}

func New(database *sql.DB) *Repositories {
	return &Repositories{
		Catalog:   &CatalogRepository{db: database},
		Sources:   &SourceRepository{db: database},
		Telemetry: &TelemetryRepository{db: database},
		Profiles:  &ProfileRepository{db: database},
		Users:     &UserRepository{db: database},
	}
}
