package controlplane

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	dbpkg "github.com/neverknowerdev/paylessforai/internal/db"
	"github.com/neverknowerdev/paylessforai/internal/updater"
)

func TestUpdateSettingsAPI(t *testing.T) {
	db, err := dbpkg.Open(context.Background(), filepath.Join(t.TempDir(), "payless.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service, err := updater.NewService(t.TempDir(), db, nil)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewWithDeps("127.0.0.1:0", time.Second, time.Second, db, nil, nil, CredentialDeps{Updates: service})
	if err != nil {
		t.Fatal(err)
	}
	get := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/updates", nil))
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), `"interval_seconds":3600`) || !strings.Contains(get.Body.String(), `"channel":"releases"`) {
		t.Fatalf("unexpected defaults: %d %s", get.Code, get.Body.String())
	}
	put := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(put, httptest.NewRequest(http.MethodPut, "/api/updates/settings", strings.NewReader(`{"enabled":false,"channel":"main","interval_seconds":1800}`)))
	if put.Code != http.StatusOK {
		t.Fatalf("save status: %d %s", put.Code, put.Body.String())
	}
	bad := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(bad, httptest.NewRequest(http.MethodPut, "/api/updates/settings", strings.NewReader(`{"enabled":true,"channel":"other","interval_seconds":1800}`)))
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("bad settings status: %d", bad.Code)
	}
}
