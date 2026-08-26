package models

type ProviderHealthRecord struct {
	RouteID                 string
	FailureCount            int64
	State                   string
	BackoffUntil, LastError *string
	UpdatedAt               string
}
