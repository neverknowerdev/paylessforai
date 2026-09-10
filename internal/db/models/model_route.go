package models

type ModelRouteRecord struct {
	ID               string
	ModelID          string
	Provider         string
	UpstreamModel    string
	Format           string
	PriceJSON        string
	CapabilitiesJSON string
	Health           string
	ObservedAt       string
	Trusted          bool
}
