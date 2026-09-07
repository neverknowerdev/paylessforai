package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/neverknowerdev/paylessforai/internal/providers"
)

// NewModelWindow is how long discovered models are highlighted in the catalog.
const NewModelWindow = 7 * 24 * time.Hour
const discoveryStateKey = "catalog.discovery.v1"

type Addition struct {
	ID      string    `json:"id"`
	AddedAt time.Time `json:"added_at"`
}

type StateStore interface {
	Get(context.Context, string) (string, bool, error)
	Set(context.Context, string, string) error
}

type discoveryState struct {
	Known     map[string]bool `json:"known"`
	Additions []Addition      `json:"additions"`
}

// Restore loads discovery history, not executable routes. The first successful
// catalog on upgrade establishes a baseline instead of marking every model new.
func (m *Manager) Restore(ctx context.Context, store StateStore) error {
	m.refreshMu.Lock()
	defer m.refreshMu.Unlock()
	value, exists, err := store.Get(ctx, discoveryStateKey)
	if err != nil {
		return err
	}
	var state discoveryState
	if exists {
		if err := json.Unmarshal([]byte(value), &state); err != nil {
			return fmt.Errorf("decode discovery history: %w", err)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.store = store
	m.known = state.Known
	m.current.Additions = state.Additions
	return nil
}

func (m *Manager) recordDiscoveries(ctx context.Context, next *Snapshot) error {
	known := make(map[string]bool, len(m.known)+len(next.Models))
	for id, value := range m.known {
		known[id] = value
	}
	additions := append([]Addition(nil), next.Additions...)
	for _, model := range next.Models {
		if !known[model.ID] && m.known != nil {
			additions = append(additions, Addition{ID: model.ID, AddedAt: next.UpdatedAt})
		}
		known[model.ID] = true
	}
	// An empty installation has no baseline until a provider returns models.
	if m.known == nil && len(next.Models) == 0 {
		return nil
	}
	if m.store != nil {
		data, err := json.Marshal(discoveryState{Known: known, Additions: additions})
		if err != nil {
			return err
		}
		if err := m.store.Set(ctx, discoveryStateKey, string(data)); err != nil {
			return fmt.Errorf("persist model discovery: %w", err)
		}
	}
	m.known = known
	next.Additions = additions
	return nil
}

func executionKey(client providers.Client) string {
	if metadata, ok := client.(providers.ClientMetadata); ok && metadata.ExecutionKey() != "" {
		return metadata.ExecutionKey()
	}
	return client.Name()
}
