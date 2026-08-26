package providers

import (
	"fmt"
	"sort"
	"strings"
)

// Definition describes the parts of a provider that are needed to create a
// client. Provider-specific discovery and protocol behavior stays behind the
// Client interface; the application lifecycle only deals with definitions.
type Definition struct {
	Name           string
	DisplayName    string
	DefaultBaseURL string
	NewClient      func(baseURL, apiKey string) Client
}

// Registry owns the provider definitions known by this build. Credentials
// select a definition at runtime, so adding a provider does not require
// changing startup or credential-loading code.
type Registry struct {
	definitions map[string]Definition
}

func NewRegistry(definitions ...Definition) *Registry {
	r := &Registry{definitions: make(map[string]Definition, len(definitions))}
	for _, definition := range definitions {
		r.Register(definition)
	}
	return r
}

func (r *Registry) Register(definition Definition) {
	if r == nil {
		return
	}
	name := normalizeName(definition.Name)
	if name == "" {
		return
	}
	if definition.NewClient == nil {
		definition.NewClient = func(baseURL, apiKey string) Client {
			return NewHTTPClient(name, baseURL, apiKey)
		}
	}
	definition.Name = name
	definition.DefaultBaseURL = strings.TrimRight(strings.TrimSpace(definition.DefaultBaseURL), "/")
	r.definitions[name] = definition
}

func (r *Registry) Definition(name string) (Definition, bool) {
	if r == nil {
		return Definition{}, false
	}
	definition, ok := r.definitions[normalizeName(name)]
	return definition, ok
}

func (r *Registry) Definitions() []Definition {
	if r == nil {
		return nil
	}
	result := make([]Definition, 0, len(r.definitions))
	for _, definition := range r.definitions {
		result = append(result, definition)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

// Resolve returns a client for a known provider, or for an explicitly named
// OpenAI-compatible provider when its credential supplies a base URL. This
// keeps the built-in registry useful while allowing users to connect future or
// self-hosted providers without a second startup path.
func (r *Registry) Resolve(name, baseURL, apiKey string) (Client, Definition, error) {
	name = normalizeName(name)
	if name == "" {
		return nil, Definition{}, fmt.Errorf("provider name is required")
	}
	definition, known := r.Definition(name)
	if !known {
		baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
		if baseURL == "" {
			return nil, Definition{}, fmt.Errorf("provider %q requires a base URL", name)
		}
		definition = Definition{Name: name, DisplayName: name, DefaultBaseURL: baseURL}
		definition.NewClient = func(url, key string) Client { return NewHTTPClient(name, url, key) }
	}
	if strings.TrimSpace(baseURL) == "" {
		baseURL = definition.DefaultBaseURL
	}
	if strings.TrimSpace(baseURL) == "" {
		return nil, Definition{}, fmt.Errorf("provider %q has no base URL", name)
	}
	return definition.NewClient(strings.TrimRight(strings.TrimSpace(baseURL), "/"), apiKey), definition, nil
}

// Builtin returns the providers whose API semantics need first-class support.
// Endpoint overrides are intended for local mocks, private gateways, and
// installations that proxy a provider API; credentials still supply the key.
func Builtin(overrides map[string]string) *Registry {
	definitions := []Definition{
		{Name: "openrouter", DisplayName: "OpenRouter", DefaultBaseURL: "https://openrouter.ai/api/v1"},
		{Name: "surplus", DisplayName: "Surplus Intelligence", DefaultBaseURL: "https://api.surplusintelligence.ai/v1"},
	}
	for index := range definitions {
		if override := strings.TrimSpace(overrides[definitions[index].Name]); override != "" {
			definitions[index].DefaultBaseURL = override
		}
	}
	return NewRegistry(definitions...)
}

func normalizeName(value string) string { return strings.ToLower(strings.TrimSpace(value)) }
