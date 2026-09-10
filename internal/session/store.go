package session

import (
	"context"
	"time"
)

// Store contains only the bounded persistence operations needed by the
// detector. It intentionally does not expose a database connection or any
// detection policy.
type Store interface {
	UpsertSession(context.Context, string, string, time.Time) error
	ResolveAndRegister(context.Context, string, []string, string, string, string, time.Time) (string, error)
}
