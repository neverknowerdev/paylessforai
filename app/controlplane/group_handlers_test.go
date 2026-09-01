package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	dbpkg "github.com/neverknowerdev/paylessforai/internal/db"
)

func TestGroupManagementAPIAndModelAlias(t *testing.T) {
	db, err := dbpkg.Open(context.Background(), filepath.Join(t.TempDir(), "payless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	server, err := New("127.0.0.1:0", time.Second, time.Second, db)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.httpServer.Handler
	_, secret, err := db.ClientAPIKeys.Create(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	createBody := `{"name":"Coding","slug":"coding","enabled":true,"stages":[{"position":0,"name":"cheapest","sources":[{"kind":"model","model_id":"model-a"}],"billing_classes":["metered"],"selection":"lowest_expected_cost"}]}`
	create := httptest.NewRecorder()
	handler.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/api/groups", strings.NewReader(createBody)))
	if create.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", create.Code, create.Body.String())
	}
	var envelope struct {
		Data struct {
			ID       string `json:"id"`
			Revision int64  `json:"revision"`
		} `json:"data"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.ID == "" || envelope.Data.Revision != 1 {
		t.Fatalf("unexpected group response: %#v", envelope)
	}
	list := httptest.NewRecorder()
	handler.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/groups", nil))
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), "coding") {
		t.Fatalf("list: %d %s", list.Code, list.Body.String())
	}
	disableBody := strings.Replace(createBody, `"enabled":true`, `"enabled":false`, 1)
	disable := httptest.NewRecorder()
	handler.ServeHTTP(disable, httptest.NewRequest(http.MethodPut, "/api/groups/"+envelope.Data.ID+"?revision=1", strings.NewReader(disableBody)))
	if disable.Code != http.StatusOK || !strings.Contains(disable.Body.String(), `"enabled":false`) {
		t.Fatalf("disable: %d %s", disable.Code, disable.Body.String())
	}
	enable := httptest.NewRecorder()
	handler.ServeHTTP(enable, httptest.NewRequest(http.MethodPut, "/api/groups/"+envelope.Data.ID+"?revision=2", strings.NewReader(createBody)))
	if enable.Code != http.StatusOK || !strings.Contains(enable.Body.String(), `"enabled":true`) {
		t.Fatalf("re-enable: %d %s", enable.Code, enable.Body.String())
	}
	models := httptest.NewRecorder()
	modelRequest := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	modelRequest.Header.Set("Authorization", "Bearer "+secret)
	handler.ServeHTTP(models, modelRequest)
	if models.Code != http.StatusOK || !strings.Contains(models.Body.String(), `"id":"coding"`) || !strings.Contains(models.Body.String(), `"paylessforai_type":"group"`) {
		t.Fatalf("models: %d %s", models.Code, models.Body.String())
	}
}

func TestGroupManagementRejectsSlugCollision(t *testing.T) {
	db, err := dbpkg.Open(context.Background(), filepath.Join(t.TempDir(), "payless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	server, err := New("127.0.0.1:0", time.Second, time.Second, db)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.httpServer.Handler
	body := `{"name":"One","slug":"same","enabled":true,"stages":[{"position":0,"sources":[{"kind":"model","model_id":"model-a"}],"billing_classes":["metered"]}]}`
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodPost, "/api/groups", strings.NewReader(body)))
	if first.Code != http.StatusCreated {
		t.Fatal(first.Body.String())
	}
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequest(http.MethodPost, "/api/groups", strings.NewReader(strings.Replace(body, "One", "Two", 1))))
	if second.Code != http.StatusUnprocessableEntity && second.Code != http.StatusConflict {
		t.Fatalf("expected collision, got %d %s", second.Code, second.Body.String())
	}
}
