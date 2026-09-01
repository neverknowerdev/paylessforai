package remoteaccess

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

type memoryStore struct {
	mu     sync.Mutex
	values map[string]string
}

func (s *memoryStore) Get(_ context.Context, key string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.values[key]
	return value, ok, nil
}
func (s *memoryStore) Set(_ context.Context, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = value
	return nil
}

type fakeNode struct {
	mu          sync.Mutex
	statuses    []*NodeStatus
	started     bool
	closed      bool
	tlsCalls    int
	funnelCalls int
}

func (n *fakeNode) Start() error { n.mu.Lock(); n.started = true; n.mu.Unlock(); return nil }
func (n *fakeNode) Status(context.Context) (*NodeStatus, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.statuses) == 0 {
		return &NodeStatus{Running: true, DNSName: "paylessforai.example.ts.net", OwnerID: "userid:1"}, nil
	}
	status := n.statuses[0]
	if len(n.statuses) > 1 {
		n.statuses = n.statuses[1:]
	}
	return status, nil
}
func (n *fakeNode) WhoIs(context.Context, string) (string, error) { return "userid:1", nil }
func (n *fakeNode) ListenTLS(string, string) (net.Listener, error) {
	n.mu.Lock()
	n.tlsCalls++
	n.mu.Unlock()
	return &fakeListener{closed: make(chan struct{})}, nil
}
func (n *fakeNode) ListenFunnel(string, string) (net.Listener, error) {
	n.mu.Lock()
	n.funnelCalls++
	n.mu.Unlock()
	return &fakeListener{closed: make(chan struct{})}, nil
}
func (n *fakeNode) Close() error { n.mu.Lock(); n.closed = true; n.mu.Unlock(); return nil }

type fakeListener struct {
	closed chan struct{}
	once   sync.Once
}

func (l *fakeListener) Accept() (net.Conn, error) {
	<-l.closed
	return nil, net.ErrClosed
}
func (l *fakeListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}
func (l *fakeListener) Addr() net.Addr { return fakeAddr("fake") }

type fakeAddr string

func (a fakeAddr) Network() string { return "fake" }
func (a fakeAddr) String() string  { return string(a) }

func TestManagerFunnelUsesPrivateAndFunnelOnlyListeners(t *testing.T) {
	store := &memoryStore{values: map[string]string{}}
	node := &fakeNode{}
	var wrapped Authorizer
	m, err := New(store, t.TempDir(), func(authorizer Authorizer) http.Handler {
		wrapped = authorizer
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "private") })
	}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "public") }), WithNodeFactory(func(string, string) Node { return node }), WithPollInterval(time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Configure(context.Background(), Config{Mode: ModeFunnel, Hostname: "PayLessForAI"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return m.Status(context.Background()).Phase == PhaseOnline })
	status := m.Status(context.Background())
	if status.BaseURL != "https://paylessforai.example.ts.net/v1" || status.DashboardURL == "" {
		t.Fatalf("unexpected online status: %#v", status)
	}
	node.mu.Lock()
	tlsCalls, funnelCalls := node.tlsCalls, node.funnelCalls
	node.mu.Unlock()
	if tlsCalls != 1 || funnelCalls != 1 {
		t.Fatalf("listeners: TLS=%d Funnel=%d", tlsCalls, funnelCalls)
	}
	if wrapped == nil {
		t.Fatal("private handler was not given an owner authorizer")
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	node.mu.Lock()
	closed := node.closed
	node.mu.Unlock()
	if !closed {
		t.Fatal("node was not closed")
	}
}

func TestManagerKeepsAuthURLInMemoryOnly(t *testing.T) {
	store := &memoryStore{values: map[string]string{}}
	node := &fakeNode{statuses: []*NodeStatus{{AuthURL: "https://login.tailscale.com/a/one"}}}
	m, err := New(store, t.TempDir(), func(Authorizer) http.Handler { return http.NotFoundHandler() }, http.NotFoundHandler(), WithNodeFactory(func(string, string) Node { return node }), WithPollInterval(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Configure(context.Background(), Config{Mode: ModePrivate, Hostname: "node"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return m.Status(context.Background()).Phase == PhaseAuthRequired })
	status := m.Status(context.Background())
	if status.Action == nil || status.Action.URL != "https://login.tailscale.com/a/one" {
		t.Fatalf("missing auth action: %#v", status)
	}
	configValue, _, _ := store.Get(context.Background(), configKey)
	resultValue, _, _ := store.Get(context.Background(), lastResultKey)
	if strings.Contains(configValue, "login.tailscale") || strings.Contains(resultValue, "login.tailscale") {
		t.Fatal("authorization URL was persisted")
	}
	_ = m.Shutdown(context.Background())
}

func TestOwnerAuthorizerFailsClosedAndDoesNotTrustHeaders(t *testing.T) {
	resolver := resolverFunc(func(context.Context, string) (string, error) { return "userid:2", nil })
	handler := OwnerAuthorizer(resolver, func() string { return "userid:1" })(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	req := httptest.NewRequest(http.MethodGet, "https://node.ts.net/", nil)
	req.Header.Set("X-Tailscale-User", "userid:1")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusForbidden {
		t.Fatalf("got %d, want 403", response.Code)
	}
}

func TestAuthURLParserAcceptsOnlyTailscaleHTTPS(t *testing.T) {
	if got := extractAuthURL(`auth URL: "https://login.tailscale.com/a/abc"`); got != "https://login.tailscale.com/a/abc" {
		t.Fatalf("got %q", got)
	}
	if got := extractAuthURL("auth URL: https://evil.example/a"); got != "" {
		t.Fatalf("accepted unsafe URL %q", got)
	}
}

func TestProtectManagementRequiresExactOriginAndToken(t *testing.T) {
	protected := ProtectManagement(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	get := httptest.NewRequest(http.MethodGet, "https://node.ts.net/", nil)
	get.Host = "node.ts.net"
	getResponse := httptest.NewRecorder()
	protected.ServeHTTP(getResponse, get)
	cookie := getResponse.Header().Get("Set-Cookie")
	token := regexp.MustCompile(`plai_csrf=([^;]+)`).FindStringSubmatch(cookie)[1]
	missing := httptest.NewRecorder()
	protected.ServeHTTP(missing, httptest.NewRequest(http.MethodPut, "https://node.ts.net/api/remote-access", nil))
	if missing.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF state returned %d", missing.Code)
	}
	req := httptest.NewRequest(http.MethodPut, "https://node.ts.net/api/remote-access", nil)
	req.Host = "node.ts.net"
	req.Header.Set("Origin", "https://node.ts.net")
	req.Header.Set("Cookie", "plai_csrf="+token)
	req.Header.Set("X-CSRF-Token", token)
	allowed := httptest.NewRecorder()
	protected.ServeHTTP(allowed, req)
	if allowed.Code != http.StatusNoContent {
		t.Fatalf("valid CSRF state returned %d", allowed.Code)
	}
}

func TestValidateConfigRejectsUnsafeHostnames(t *testing.T) {
	for _, hostname := range []string{"../node", "-node", "node-", "NODE.example", strings.Repeat("a", 64)} {
		if _, err := ValidateConfig(Config{Mode: ModePrivate, Hostname: hostname}); err == nil {
			t.Errorf("hostname %q was accepted", hostname)
		}
	}
	if config, err := ValidateConfig(Config{Mode: ModeDisabled}); err != nil || config.Hostname != defaultHost {
		t.Fatalf("default config: %#v, %v", config, err)
	}
}

type resolverFunc func(context.Context, string) (string, error)

func (f resolverFunc) WhoIs(ctx context.Context, addr string) (string, error) { return f(ctx, addr) }

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition did not become true")
}
