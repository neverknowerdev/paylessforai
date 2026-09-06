package providers

import "testing"

func TestBuiltinRegistryResolvesKnownProvidersWithoutCredentials(t *testing.T) {
	registry := Builtin(map[string]string{"openrouter": "http://mock/openrouter/v1"})
	client, definition, err := registry.Resolve("OPENROUTER", "", "key")
	if err != nil {
		t.Fatal(err)
	}
	if client.Name() != "openrouter" || definition.DisplayName != "OpenRouter" {
		t.Fatalf("unexpected resolution: client=%q definition=%#v", client.Name(), definition)
	}
	for _, expected := range []struct {
		name string
		url  string
	}{
		{name: "opencode-zen", url: "https://opencode.ai/zen/v1"},
		{name: "opencode-go", url: "https://opencode.ai/zen/go/v1"},
	} {
		_, definition, err := registry.Resolve(expected.name, "", "key")
		if err != nil || definition.DefaultBaseURL != expected.url || definition.DefaultAccessMode != "subscription" {
			t.Fatalf("unexpected %s definition: %#v, %v", expected.name, definition, err)
		}
	}
}

func TestRegistryResolvesCustomOpenAICompatibleProvider(t *testing.T) {
	registry := Builtin(nil)
	client, definition, err := registry.Resolve("local-llm", "http://127.0.0.1:9999/v1", "key")
	if err != nil {
		t.Fatal(err)
	}
	if client.Name() != "local-llm" || definition.DisplayName != "local-llm" {
		t.Fatalf("unexpected custom resolution: client=%q definition=%#v", client.Name(), definition)
	}
}

func TestRegistryRejectsUnknownProviderWithoutEndpoint(t *testing.T) {
	if _, _, err := Builtin(nil).Resolve("local-llm", "", "key"); err == nil {
		t.Fatal("expected a base URL requirement for unknown providers")
	}
}
