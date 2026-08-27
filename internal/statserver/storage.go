package statserver

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/lib/pq"
)

func openDB(url string) (*sql.DB, error) {
	db, err := sql.Open("postgres", url)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(12)
	db.SetMaxIdleConns(4)
	if err = db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}
func migrate(db *sql.DB) error {
	if _, err := db.ExecContext(context.Background(), migrationSQL); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}
