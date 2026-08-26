package models

type ModelRecord struct {
	ID              string
	DisplayName     string
	ContextLength   int64
	MaxOutputTokens int64
	MetadataJSON    string
	ObservedAt      string
}
