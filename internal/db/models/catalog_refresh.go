package models

type CatalogRefresh struct {
	ID          string
	Provider    string
	Status      string
	StartedAt   string
	CompletedAt *string
	Error       *string
}
