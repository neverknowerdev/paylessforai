package catalog

import (
	"context"
	"fmt"
	"regexp"
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
	clients   []providers.Client
	mu        sync.RWMutex
	current   Snapshot
	blocked   map[string]*time.Time
	onRefresh func(context.Context, []matcher.Route) error
}

func New(clients []providers.Client) *Manager {
	return &Manager{clients: append([]providers.Client(nil), clients...), blocked: make(map[string]*time.Time)}
}

func (m *Manager) SetProviderBlocked(provider string, until *time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.blocked == nil {
		m.blocked = make(map[string]*time.Time)
	}
	if until == nil {
		m.blocked[provider] = nil
		return
	}
	copy := until.UTC()
	m.blocked[provider] = &copy
}

func (m *Manager) ProviderBlocked(provider string, now time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	until, ok := m.blocked[provider]
	if !ok {
		return false
	}
	if until != nil && !now.Before(*until) {
		delete(m.blocked, provider)
		return false
	}
	return true
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

// SetRefreshHook registers the single integration point for work that must
// happen whenever provider discovery produces new routes (for example,
// syncing groups that opted into future providers).
func (m *Manager) SetRefreshHook(hook func(context.Context, []matcher.Route) error) {
	m.mu.Lock()
	m.onRefresh = hook
	m.mu.Unlock()
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
	previous := m.Snapshot()
	type discovered struct {
		provider string
		models   []providers.Model
		client   providers.Client
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
		all = append(all, discovered{provider: client.Name(), models: models, client: client})
	}
	if len(all) == 0 {
		return fmt.Errorf("all provider catalog refreshes failed: %s", strings.Join(failures, "; "))
	}
	now := time.Now().UTC()
	modelMap := map[string]Model{}
	routes := make([]matcher.Route, 0)
	for _, batch := range all {
		executionKey, credentialID := batch.provider, ""
		billingClass := matcher.BillingMetered
		if metadata, ok := batch.client.(providers.ClientMetadata); ok {
			if value := metadata.ExecutionKey(); value != "" {
				executionKey = value
			}
			credentialID = metadata.CredentialID()
			if value := metadata.BillingClass(); value != "" {
				billingClass = value
			}
		}
		for _, model := range batch.models {
			// A logical model is derived from the upstream ID itself, rather
			// than from whichever provider happened to be scanned first. This
			// lets a user add OpenCode, OpenCode Go, and OpenRouter in any order.
			logical := logicalModel(model.ID)
			// Providers classify free routes while parsing their native catalog
			// metadata. Do not infer free from zero token prices here: media APIs
			// commonly expose prompt/completion as zero while charging per image,
			// audio minute, video, or job.
			free := model.Free || modelIDHasFreeRouteQualifier(model.ID)
			candidate := Model{ID: logical, Name: canonicalModelName(logical), Free: free, ContextLength: model.ContextLength, MaxCompletionTokens: model.MaxCompletionTokens, SupportedParameters: append([]string(nil), model.SupportedParameters...), InputModalities: append([]string(nil), model.InputModalities...), OutputModalities: append([]string(nil), model.OutputModalities...), Tags: append([]string(nil), model.Tags...)}
			if existing, ok := modelMap[logical]; ok {
				modelMap[logical] = mergeModels(existing, candidate)
			} else {
				modelMap[logical] = candidate
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
			routeBilling := billingClass
			if free {
				routeBilling = matcher.BillingFree
			}
			routes = append(routes, matcher.Route{ID: executionKey + ":" + model.ID, Provider: batch.provider, LogicalModel: logical, UpstreamModel: model.ID, Free: free, Price: model.Pricing, PriceAvailable: model.PriceAvailable, OfficialPrice: model.OfficialPricing, OfficialPriceAvailable: model.OfficialPriceAvailable, CredentialID: credentialID, ExecutionKey: executionKey, BillingClass: routeBilling, Capabilities: matcher.Capabilities{Protocols: protocols, Parameters: parameters, Tools: parameters["tools"], StructuredOutput: parameters["response_format"] || parameters["structured_outputs"], MaxContext: model.ContextLength, MaxOutput: model.MaxCompletionTokens, InputModalities: inputModalities, OutputModalities: outputModalities, Tags: append([]string(nil), model.Tags...)}, Health: matcher.HealthHealthy, Trusted: true})
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
	hook := m.onRefresh
	m.mu.Unlock()
	if hook != nil {
		known := make(map[string]bool, len(previous.Routes))
		for _, route := range previous.Routes {
			known[routeProviderModelKey(route)] = true
		}
		newRoutes := make([]matcher.Route, 0)
		seen := make(map[string]bool)
		for _, route := range routes {
			key := routeProviderModelKey(route)
			if known[key] || seen[key] {
				continue
			}
			seen[key] = true
			newRoutes = append(newRoutes, route)
		}
		if len(newRoutes) > 0 {
			if err := hook(ctx, newRoutes); err != nil {
				failures = append(failures, "group provider sync: "+err.Error())
			}
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("partial provider catalog refresh: %s", strings.Join(failures, "; "))
	}
	return nil
}

func routeProviderModelKey(route matcher.Route) string {
	return route.Provider + "\x00" + route.LogicalModel
}

func (m *Manager) Client(provider string) providers.Client {
	for _, client := range m.Clients() {
		if client.Name() == provider {
			return client
		}
	}
	return nil
}

func (m *Manager) ClientForRoute(route matcher.Route) providers.Client {
	if route.ExecutionKey == "" {
		return m.Client(route.Provider)
	}
	for _, client := range m.Clients() {
		if metadata, ok := client.(providers.ClientMetadata); ok && metadata.ExecutionKey() == route.ExecutionKey {
			return client
		}
	}
	return m.Client(route.Provider)
}

var hyphenatedVersion = regexp.MustCompile(`([0-9]+)-([0-9]+)`)

// logicalModel produces a provider-neutral, stable public slug. Namespaces
// such as "meta/" identify the publisher's upstream naming scheme; they are
// not part of the model a PayLessForAI client requests. Free is a route tier,
// not a distinct model.
func logicalModel(id string) string {
	value := strings.ToLower(strings.TrimSpace(id))
	// Catalogs occasionally use multiple path components. The final component
	// is the provider's model alias; preceding components are namespace or
	// publisher hints and deliberately do not affect identity.
	if index := strings.LastIndexByte(value, '/'); index >= 0 {
		value = value[index+1:]
	}
	if strings.HasSuffix(value, ":free") {
		value = strings.TrimSuffix(value, ":free")
	} else if strings.HasSuffix(value, "-free") {
		// OpenCode exposes free offers as bare `model-free` aliases. Do not
		// repeatedly trim: `s2.1-pro-free:free` is a distinct base slug with
		// an additional :free route qualifier.
		value = strings.TrimSuffix(value, "-free")
	}
	value = strings.NewReplacer("_", "-", " ", "-").Replace(value)
	value = strings.Trim(value, "-.")
	for {
		normalized := hyphenatedVersion.ReplaceAllString(value, "$1.$2")
		if normalized == value {
			break
		}
		value = normalized
	}
	return value
}

func modelIDHasFreeRouteQualifier(id string) bool {
	value := strings.ToLower(strings.TrimSpace(id))
	return strings.HasSuffix(value, ":free") || strings.HasSuffix(value, "-free")
}

func canonicalModelName(slug string) string {
	words := strings.FieldsFunc(slug, func(r rune) bool { return r == '-' || r == '_' || r == ' ' })
	initialisms := map[string]string{"api": "API", "glm": "GLM", "gpt": "GPT", "llm": "LLM"}
	for index, word := range words {
		if replacement, ok := initialisms[word]; ok {
			words[index] = replacement
			continue
		}
		if word != "" {
			words[index] = strings.ToUpper(word[:1]) + word[1:]
		}
	}
	return strings.Join(words, " ")
}

func mergeModels(current, candidate Model) Model {
	if current.Name == "" || (candidate.Name != "" && candidate.Name < current.Name) {
		current.Name = candidate.Name
	}
	current.Free = current.Free || candidate.Free
	current.ContextLength = max(current.ContextLength, candidate.ContextLength)
	current.MaxCompletionTokens = max(current.MaxCompletionTokens, candidate.MaxCompletionTokens)
	current.SupportedParameters = mergeStrings(current.SupportedParameters, candidate.SupportedParameters)
	current.InputModalities = mergeStrings(current.InputModalities, candidate.InputModalities)
	current.OutputModalities = mergeStrings(current.OutputModalities, candidate.OutputModalities)
	current.Tags = mergeStrings(current.Tags, candidate.Tags)
	return current
}

func mergeStrings(left, right []string) []string {
	seen := make(map[string]struct{}, len(left)+len(right))
	for _, value := range append(append([]string(nil), left...), right...) {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" {
			seen[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
