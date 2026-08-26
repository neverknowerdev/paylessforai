package repositories

import (
	"database/sql"

	"github.com/stephenafamo/bob"
)

// bobRepository is the small common dependency shared by table repositories.
// All SQL is built by Bob's generated tables; repository methods only map
// application records to generated persistence models.
type bobRepository struct {
	exec bob.Executor
}

func nullableString(value *string) sql.Null[string] {
	if value == nil {
		return sql.Null[string]{}
	}
	return sql.Null[string]{V: *value, Valid: true}
}

func nullableInt64(value *int64) sql.Null[int64] {
	if value == nil {
		return sql.Null[int64]{}
	}
	return sql.Null[int64]{V: *value, Valid: true}
}

func stringPointer(value sql.Null[string]) *string {
	if !value.Valid {
		return nil
	}
	result := value.V
	return &result
}

func int64Pointer(value sql.Null[int64]) *int64 {
	if !value.Valid {
		return nil
	}
	result := value.V
	return &result
}

func nullableStringPointer(value *string) *sql.Null[string] {
	converted := nullableString(value)
	return &converted
}

func pointerInt64(value int64) *int64 { return &value }

func nullableStringValue(value sql.Null[string]) any {
	if !value.Valid {
		return nil
	}
	return value.V
}

func nullableInt64Value(value sql.Null[int64]) any {
	if !value.Valid {
		return nil
	}
	return value.V
}
