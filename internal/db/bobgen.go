package db

//go:generate sh -c "cd ../.. && bobgen-sql -c bobgen.yaml"

// This file documents the generated persistence boundary. The generated
// models under bob/models are intentionally separate from db/models: the
// former are database-facing Bob types, while the latter are thin
// application-facing records.
