package session

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/neverknowerdev/paylessforai/internal/ids"
	"github.com/neverknowerdev/paylessforai/internal/wire"
)

const storeTimeout = 25 * time.Millisecond

type Input struct {
	ClientKeyID string
	Format      wire.Format
	Headers     http.Header
	Body        []byte
}

type Detector struct {
	store Store
	key   []byte
	now   func() time.Time
	newID func() string
}

func NewDetector(store Store, key []byte) *Detector {
	return &Detector{store: store, key: append([]byte(nil), key...), now: time.Now, newID: ids.New}
}

// DetectSessionID always returns an ID and never exposes store or parsing
// failures to the request handler.
func (d *Detector) DetectSessionID(ctx context.Context, input Input) string {
	if value, ok := selectedHeader(input.Headers); ok {
		if d.store != nil && input.ClientKeyID != "" {
			storeCtx, cancel := context.WithTimeout(ctx, storeTimeout)
			if err := d.store.UpsertSession(storeCtx, input.ClientKeyID, value, d.now().UTC()); err != nil {
				slog.Warn("session persistence failed", "operation", "upsert")
			}
			cancel()
		}
		return value
	}
	fresh := d.newID()
	if input.ClientKeyID == "" || len(d.key) == 0 {
		return fresh
	}
	items, ok := extractHistory(input.Format, input.Body)
	if !ok {
		return fresh
	}
	hashes := prefixHashes(d.key, input.ClientKeyID, items)
	if len(hashes) == 0 || d.store == nil {
		return fresh
	}
	anchor := hashes[0]
	fingerprint := hashes[len(hashes)-1]
	for left, right := 0, len(hashes)-1; left < right; left, right = left+1, right-1 {
		hashes[left], hashes[right] = hashes[right], hashes[left]
	}
	storeCtx, cancel := context.WithTimeout(ctx, storeTimeout)
	selected, err := d.store.ResolveAndRegister(storeCtx, input.ClientKeyID, hashes, anchor, fingerprint, fresh, d.now().UTC())
	cancel()
	if err != nil {
		slog.Warn("session persistence failed", "operation", "resolve_and_register")
		return fresh
	}
	if selected == "" {
		return fresh
	}
	return selected
}
