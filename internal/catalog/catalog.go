package catalog

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/neverknowerdev/paylessforai/internal/matcher"
	"github.com/neverknowerdev/paylessforai/internal/providers"
)

type Model struct {
	ID                  string
	Name                string
	Free                bool
	ContextLength       int64
	MaxCompletionTokens int64
	SupportedParameters []string
	InputModalities     []string
	OutputModalities    []string
	Tags                []string
}

type Snapshot struct {
	UpdatedAt time.Time
	Models    []Model
	Routes    []matcher.Route
}

type Manager struct {
	clients []providers.Client
	mu      sync.RWMutex
	current Snapshot
}

func New(clients []providers.Client) *Manager {
	return &Manager{clients: append([]providers.Client(nil), clients...)}
}

func (m *Manager) SetClients(clients []providers.Client) {
	m.mu.Lock()
	m.clients = append([]providers.Client(nil), clients...)
	m.mu.Unlock()
}

func (m *Manager) Clients() []providers.Client {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]providers.Client(nil), m.clients...)
}

func (m *Manager) Snapshot() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := m.current
	result.Models = append([]Model(nil), result.Models...)
	result.Routes = append([]matcher.Route(nil), result.Routes...)
	return result
}

func (m *Manager) Refresh(ctx context.Context) error {
	type discovered struct {
		provider string
		models   []providers.Model
	}
	clients := m.Clients()
	all := make([]discovered, 0, len(clients))
	var failures []string
	for _, client := range clients {
		models, err := client.Discover(ctx)
		if err != nil {
			failures = append(failures, client.Name()+": "+err.Error())
			continue
		}
		all = append(all, discovered{provider: client.Name(), models: models})
	}
	if len(all) == 0 {
		return fmt.Errorf("all provider catalog refreshes failed: %s", strings.Join(failures, "; "))
	}
	openRouterIDs := make([]string, 0)
	for _, batch := range all {
		if batch.provider == "openrouter" {
			for _, model := range batch.models {
				openRouterIDs = append(openRouterIDs, model.ID)
			}
		}
	}
	now := time.Now().UTC()
	modelMap := map[string]Model{}
	routes := make([]matcher.Route, 0)
	for _, batch := range all {
		for _, model := range batch.models {
			logical := logicalModel(batch.provider, model.ID, openRouterIDs)
			// Providers classify free routes while parsing their native catalog
			// metadata. Do not infer free from zero token prices here: media APIs
			// commonly expose prompt/completion as zero while charging per image,
			// audio minute, video, or job.
			free := model.Free
			if _, ok := modelMap[logical]; !ok {
				modelMap[logical] = Model{ID: logical, Name: model.Name, Free: free, ContextLength: model.ContextLength, MaxCompletionTokens: model.MaxCompletionTokens, SupportedParameters: append([]string(nil), model.SupportedParameters...), InputModalities: append([]string(nil), model.InputModalities...), OutputModalities: append([]string(nil), model.OutputModalities...), Tags: append([]string(nil), model.Tags...)}
			} else if free {
				merged := modelMap[logical]
				merged.Free = true
				modelMap[logical] = merged
			}
			protocols := map[matcher.Protocol]bool{matcher.ProtocolChatCompletions: true, matcher.ProtocolResponses: true, matcher.ProtocolAnthropic: true}
			parameters := make(map[string]bool, len(model.SupportedParameters))
			for _, parameter := range model.SupportedParameters {
				parameters[parameter] = true
			}
			inputModalities := make(map[string]bool, len(model.InputModalities))
			for _, modality := range model.InputModalities {
				inputModalities[strings.ToLower(modality)] = true
			}
			outputModalities := make(map[string]bool, len(model.OutputModalities))
			for _, modality := range model.OutputModalities {
				outputModalities[strings.ToLower(modality)] = true
			}
			routes = append(routes, matcher.Route{ID: batch.provider + ":" + model.ID, Provider: batch.provider, LogicalModel: logical, UpstreamModel: model.ID, Free: free, Price: model.Pricing, PriceAvailable: model.PriceAvailable, Capabilities: matcher.Capabilities{Protocols: protocols, Parameters: parameters, Tools: parameters["tools"], StructuredOutput: parameters["response_format"] || parameters["structured_outputs"], MaxContext: model.ContextLength, MaxOutput: model.MaxCompletionTokens, InputModalities: inputModalities, OutputModalities: outputModalities, Tags: append([]string(nil), model.Tags...)}, Health: matcher.HealthHealthy, Trusted: true})
		}
	}
	models := make([]Model, 0, len(modelMap))
	for _, model := range modelMap {
		models = append(models, model)
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	sort.Slice(routes, func(i, j int) bool { return routes[i].ID < routes[j].ID })
	m.mu.Lock()
	m.current = Snapshot{UpdatedAt: now, Models: models, Routes: routes}
	m.mu.Unlock()
	if len(failures) > 0 {
		return fmt.Errorf("partial provider catalog refresh: %s", strings.Join(failures, "; "))
	}
	return nil
}

func (m *Manager) Client(provider string) providers.Client {
	for _, client := range m.Clients() {
		if client.Name() == provider {
			return client
		}
	}
	return nil
}

func logicalModel(provider, id string, openRouterIDs []string) string {
	if provider == "openrouter" {
		return strings.TrimSuffix(id, ":free")
	}
	for _, openRouterID := range openRouterIDs {
		canonical := strings.TrimSuffix(openRouterID, ":free")
		if canonical == id || strings.HasSuffix(canonical, "/"+id) {
			return canonical
		}
	}
	return id
}
