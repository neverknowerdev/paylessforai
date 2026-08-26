package models

type CatalogRefresh struct {
	ID, Provider, Status, StartedAt string
	CompletedAt, Error              *string
}
