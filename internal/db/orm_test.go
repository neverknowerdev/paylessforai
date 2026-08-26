package db

import "testing"

func TestRebindPostgresPlaceholders(t *testing.T) {
	query := "INSERT INTO records(first, second, third, tenth) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"
	want := "INSERT INTO records(first, second, third, tenth) VALUES($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)"
	if got := rebind(query, PostgresDialect); got != want {
		t.Fatalf("rebind() = %q, want %q", got, want)
	}
}

func TestRebindLeavesSQLitePlaceholdersUnchanged(t *testing.T) {
	query := "SELECT * FROM records WHERE id = ?"
	if got := rebind(query, SQLiteDialect); got != query {
		t.Fatalf("rebind() = %q, want %q", got, query)
	}
}
