package groups

import (
	"context"
	"sort"
	"sync"
)

type Loader interface {
	ListGroups(context.Context) ([]Definition, error)
}

type Manager struct {
	loader      Loader
	mu          sync.RWMutex
	definitions map[string]Definition
	bySlug      map[string]string
}

func NewManager(loader Loader) *Manager {
	return &Manager{loader: loader, definitions: map[string]Definition{}, bySlug: map[string]string{}}
}

func (m *Manager) Reload(ctx context.Context) error {
	if m == nil || m.loader == nil {
		return nil
	}
	items, err := m.loader.ListGroups(ctx)
	if err != nil {
		return err
	}
	definitions := map[string]Definition{}
	bySlug := map[string]string{}
	for _, item := range items {
		definitions[item.ID] = item
		bySlug[NormalizeSlug(item.Slug)] = item.ID
	}
	m.mu.Lock()
	m.definitions, m.bySlug = definitions, bySlug
	m.mu.Unlock()
	return nil
}

func (m *Manager) Snapshot() []Definition {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Definition, 0, len(m.definitions))
	for _, value := range m.definitions {
		out = append(out, cloneDefinition(value))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Slug == out[j].Slug {
			return out[i].ID < out[j].ID
		}
		return out[i].Slug < out[j].Slug
	})
	return out
}
func (m *Manager) FindBySlug(slug string) (Definition, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, ok := m.bySlug[NormalizeSlug(slug)]
	if !ok {
		return Definition{}, false
	}
	value, ok := m.definitions[id]
	return cloneDefinition(value), ok
}
func (m *Manager) DefinitionsByID() map[string]Definition {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := map[string]Definition{}
	for id, value := range m.definitions {
		out[id] = cloneDefinition(value)
	}
	return out
}

func cloneDefinition(value Definition) Definition {
	value.Stages = append([]Stage(nil), value.Stages...)
	for i := range value.Stages {
		value.Stages[i].Sources = append([]Source(nil), value.Stages[i].Sources...)
		for j := range value.Stages[i].Sources {
			value.Stages[i].Sources[j].ProviderNames = append([]string(nil), value.Stages[i].Sources[j].ProviderNames...)
		}
		value.Stages[i].ProviderNames = append([]string(nil), value.Stages[i].ProviderNames...)
		value.Stages[i].BillingClasses = append([]BillingClass(nil), value.Stages[i].BillingClasses...)
	}
	return value
}
