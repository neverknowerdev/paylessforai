package models

type ModelRouteRecord struct {
	ID, ModelID, Provider, UpstreamModel, Protocol, PriceJSON, CapabilitiesJSON, Health, ObservedAt string
	Trusted                                                                                         bool
}
