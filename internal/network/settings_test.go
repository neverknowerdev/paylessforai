package network

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

type memoryStore struct{ values map[string]string }

type failingStore struct{ memoryStore }

func (f *failingStore) Set(context.Context, string, string) error { return errors.New("storage failed") }

func (m *memoryStore) Get(_ context.Context, key string) (string, bool, error) {
	value, ok := m.values[key]
	return value, ok, nil
}

func (m *memoryStore) Set(_ context.Context, key, value string) error {
	if m.values == nil {
		m.values = map[string]string{}
	}
	m.values[key] = value
	return nil
}

type fakeListener struct {
	address net.Addr
	closed  bool
}

func (f *fakeListener) Accept() (net.Conn, error) { return nil, errors.New("not implemented") }
func (f *fakeListener) Close() error              { f.closed = true; return nil }
func (f *fakeListener) Addr() net.Addr            { return f.address }

func fakeListen(fail map[int]bool, calls *[]string) func(string, string) (net.Listener, error) {
	return func(_, address string) (net.Listener, error) {
		*calls = append(*calls, address)
		_, portText, _ := net.SplitHostPort(address)
		port, _ := strconv.Atoi(portText)
		if fail[port] {
			return nil, &net.OpError{Op: "listen", Net: "tcp", Addr: &net.TCPAddr{IP: net.ParseIP(DefaultHost), Port: port}, Err: syscall.EADDRINUSE}
		}
		if port == 0 {
			port = 42001
		}
		return &fakeListener{address: &net.TCPAddr{IP: net.ParseIP(DefaultHost), Port: port}}, nil
	}
}

func TestBootstrapFirstRunSkipsOccupiedCandidatesAndPersistsBoundPort(t *testing.T) {
	store := &memoryStore{}
	var calls []string
	service := NewService(store)
	service.listen = fakeListen(map[int]bool{PreferredPort: true}, &calls)

	listener, state, err := service.Bootstrap(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if state.ActivePort != PreferredPort+1 || state.ConfiguredPort != PreferredPort+1 || !state.HasConfigured {
		t.Fatalf("unexpected state: %#v", state)
	}
	if store.values[SettingListenPort] != strconv.Itoa(PreferredPort+1) {
		t.Fatalf("expected persisted port, got %#v", store.values)
	}
	if len(calls) != 2 || !strings.HasSuffix(calls[0], ":9472") || !strings.HasSuffix(calls[1], ":9473") {
		t.Fatalf("unexpected bind sequence: %#v", calls)
	}
}

func TestBootstrapReusesPersistedPort(t *testing.T) {
	store := &memoryStore{values: map[string]string{SettingListenPort: "9488"}}
	var calls []string
	service := NewService(store)
	service.listen = fakeListen(nil, &calls)

	listener, state, err := service.Bootstrap(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if state.ActivePort != 9488 || state.ConfiguredPort != 9488 || state.RestartRequired {
		t.Fatalf("unexpected state: %#v", state)
	}
	if len(calls) != 1 || !strings.HasSuffix(calls[0], ":9488") {
		t.Fatalf("unexpected bind sequence: %#v", calls)
	}
}

func TestBootstrapExplicitOverrideDoesNotPersist(t *testing.T) {
	store := &memoryStore{values: map[string]string{SettingListenPort: "9488"}}
	var calls []string
	service := NewService(store)
	service.listen = fakeListen(nil, &calls)

	listener, state, err := service.Bootstrap(context.Background(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if state.ActivePort != 42001 || !state.OverrideActive || !state.RestartRequired || state.ConfiguredPort != 9488 {
		t.Fatalf("unexpected override state: %#v", state)
	}
	if store.values[SettingListenPort] != "9488" {
		t.Fatalf("override changed persisted setting: %#v", store.values)
	}
}

func TestBootstrapPersistedPortConflictPreservesSetting(t *testing.T) {
	store := &memoryStore{values: map[string]string{SettingListenPort: "9488"}}
	service := NewService(store)
	service.listen = fakeListen(map[int]bool{9488: true}, new([]string))

	_, _, err := service.Bootstrap(context.Background(), "")
	if !errors.Is(err, ErrPortInUse) {
		t.Fatalf("expected port conflict, got %v", err)
	}
	if store.values[SettingListenPort] != "9488" {
		t.Fatalf("conflict changed persisted setting: %#v", store.values)
	}
}

func TestBootstrapFallsBackToEphemeralPort(t *testing.T) {
	store := &memoryStore{}
	var calls []string
	fail := map[int]bool{}
	for port := PreferredPort; port <= CandidateLastPort; port++ {
		fail[port] = true
	}
	service := NewService(store)
	service.listen = fakeListen(fail, &calls)

	listener, state, err := service.Bootstrap(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if state.ActivePort != 42001 || store.values[SettingListenPort] != "42001" {
		t.Fatalf("expected ephemeral fallback, state=%#v values=%#v", state, store.values)
	}
	if len(calls) != CandidateLastPort-PreferredPort+2 || !strings.HasSuffix(calls[len(calls)-1], ":0") {
		t.Fatalf("unexpected fallback sequence: %#v", calls)
	}
}

func TestBootstrapClosesListenerWhenPersistenceFails(t *testing.T) {
	store := &failingStore{}
	var bound *fakeListener
	service := NewServiceWithListen(store, func(_, _ string) (net.Listener, error) {
		bound = &fakeListener{address: &net.TCPAddr{IP: net.ParseIP(DefaultHost), Port: PreferredPort}}
		return bound, nil
	})
	if _, _, err := service.Bootstrap(context.Background(), ""); err == nil {
		t.Fatal("expected persistence failure")
	}
	if bound == nil || !bound.closed {
		t.Fatalf("listener was not closed after persistence failure: %#v", bound)
	}
}

func TestSetPortRejectsOccupiedPortAndPersistsAvailablePort(t *testing.T) {
	store := &memoryStore{values: map[string]string{SettingListenPort: "9488"}}
	var calls []string
	service := NewService(store)
	service.listen = fakeListen(map[int]bool{9490: true}, &calls)
	listener, _, err := service.Bootstrap(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if _, err := service.SetPort(context.Background(), 9490); !errors.Is(err, ErrPortInUse) {
		t.Fatalf("expected occupied port error, got %v", err)
	}
	state, err := service.SetPort(context.Background(), 9491)
	if err != nil {
		t.Fatal(err)
	}
	if state.ConfiguredPort != 9491 || !state.RestartRequired || store.values[SettingListenPort] != "9491" {
		t.Fatalf("unexpected updated state: %#v values=%#v", state, store.values)
	}
}

func TestSetPortAllowsKeepingTheActiveOverridePort(t *testing.T) {
	store := &memoryStore{}
	service := NewServiceWithListen(store, func(_, _ string) (net.Listener, error) {
		return &fakeListener{address: &net.TCPAddr{IP: net.ParseIP(DefaultHost), Port: 42001}}, nil
	})
	listener, state, err := service.Bootstrap(context.Background(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	state, err = service.SetPort(context.Background(), 42001)
	if err != nil {
		t.Fatal(err)
	}
	if state.ConfiguredPort != 42001 || store.values[SettingListenPort] != "42001" {
		t.Fatalf("unexpected state after keeping active port: %#v", state)
	}
}

func TestValidatePort(t *testing.T) {
	for _, port := range []int{0, 1, MinUserPort - 1, MaxUserPort + 1} {
		if err := ValidatePort(port); !errors.Is(err, ErrInvalidPort) {
			t.Fatalf("port %d: expected invalid port, got %v", port, err)
		}
	}
	for _, port := range []int{MinUserPort, PreferredPort, MaxUserPort} {
		if err := ValidatePort(port); err != nil {
			t.Fatalf("port %d: unexpected error %v", port, err)
		}
	}
}
