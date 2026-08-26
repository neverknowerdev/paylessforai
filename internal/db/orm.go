package db

import "database/sql"

// ORM is the small database execution boundary shared by repositories. It
// deliberately embeds database/sql so repositories can be tested with the
// same interface while keeping connection and transaction ownership in db.
type ORM struct{ *sql.DB }

func NewORM(database *sql.DB) *ORM { return &ORM{DB: database} }
