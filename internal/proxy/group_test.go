package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/neverknowerdev/paylessforai/internal/groups"
	"github.com/neverknowerdev/paylessforai/internal/matcher"
	"github.com/neverknowerdev/paylessforai/internal/providers"
)

func TestProxyRoutesGroupSlugAndPersistsResolution(t *testing.T) {
	provider := &fakeProvider{name: "surplus", models: []providers.Model{model("model-a", 1, 1)}}
	proxy, store, secret := testProxy(t, provider)
	defer store.Close()
	manager := groups.NewManager(store)
	definition := groups.Definition{Name: "Coding", Slug: "coding", Enabled: true, Stages: []groups.Stage{{Position: 0, Name: "cheapest", Sources: []groups.Source{{Kind: groups.SourceModel, ModelID: "model-a"}}, BillingClasses: []groups.BillingClass{groups.BillingMetered}, Selection: groups.SelectionLowestExpectedCost}}}
	if _, err := store.SaveGroup(context.Background(), definition, nil); err != nil {
		t.Fatal(err)
	}
	if err := manager.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	proxy.SetGroups(manager)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"coding","messages":[{"role":"user","content":"hello"}]}`))
	request.Header.Set("Authorization", "Bearer "+secret)
	response := httptest.NewRecorder()
	proxy.ServeHTTP(response, request, matcher.ProtocolChatCompletions)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.modelsSeen) != 1 || provider.modelsSeen[0] != "model-a" {
		t.Fatalf("expected concrete upstream model, got %#v", provider.modelsSeen)
	}
	var resolvedID, selected string
	if err := store.DB().QueryRow(`SELECT resolved_group_id, selected_logical_model FROM proxy_requests`).Scan(&resolvedID, &selected); err != nil {
		t.Fatal(err)
	}
	if resolvedID == "" || selected != "model-a" {
		t.Fatalf("resolution was not persisted: %q %q", resolvedID, selected)
	}
}
