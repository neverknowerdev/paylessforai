package db

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
)

type SQLDialect uint8

const (
	SQLiteDialect SQLDialect = iota
	PostgresDialect
)

// ORM is the small database execution boundary shared by repositories. It
// keeps driver-specific placeholder syntax out of repository SQL while
// retaining database/sql connection ownership in the db package.
type ORM struct {
	*sql.DB
	dialect SQLDialect
}

func NewORM(database *sql.DB) *ORM {
	return &ORM{DB: database, dialect: SQLiteDialect}
}

func NewPostgresORM(database *sql.DB) *ORM {
	return &ORM{DB: database, dialect: PostgresDialect}
}

func (o *ORM) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return o.DB.ExecContext(ctx, rebind(query, o.dialect), args...)
}

func (o *ORM) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return o.DB.QueryContext(ctx, rebind(query, o.dialect), args...)
}

func (o *ORM) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return o.DB.QueryRowContext(ctx, rebind(query, o.dialect), args...)
}

func rebind(query string, dialect SQLDialect) string {
	if dialect != PostgresDialect || !strings.Contains(query, "?") {
		return query
	}
	var result strings.Builder
	result.Grow(len(query) + 8)
	placeholder := 0
	for _, character := range query {
		if character == '?' {
			placeholder++
			result.WriteByte('$')
			result.WriteString(strconv.Itoa(placeholder))
			continue
		}
		result.WriteRune(character)
	}
	return result.String()
}
