package models

type ModelRecord struct {
	ID, DisplayName                string
	ContextLength, MaxOutputTokens int64
	MetadataJSON, ObservedAt       string
}
