// Package network owns the local HTTP listener's persisted configuration.
//
// A listener is selected by binding it, not by probing and binding later. The
// selected listener remains owned by the caller for the lifetime of the HTTP
// server, which prevents a check-then-bind race.
package network

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

const (
	SettingListenPort = "server.listen_port"
	DefaultHost       = "127.0.0.1"
	PreferredPort     = 9472
	CandidateLastPort = 9499
	MinUserPort       = 1024
	MaxUserPort       = 65535
)

var (
	ErrPortInUse       = errors.New("listen port is already in use")
	ErrInvalidPort     = errors.New("invalid listen port")
	ErrInvalidStored   = errors.New("stored listen port is invalid")
	ErrSettingsMissing = errors.New("network settings are unavailable")
)

type Store interface {
	Get(context.Context, string) (string, bool, error)
	Set(context.Context, string, string) error
}

type State struct {
	Host            string
	ActivePort      int
	ConfiguredPort  int
	HasConfigured   bool
	OverrideActive  bool
	RestartRequired bool
}

func (s State) ActiveAddress() string {
	return net.JoinHostPort(s.Host, strconv.Itoa(s.ActivePort))
}

func (s State) BaseURL() string {
	return "http://" + s.ActiveAddress() + "/v1"
}

type Service struct {
	store   Store
	host    string
	listen  func(string, string) (net.Listener, error)
	mu      sync.RWMutex
	writeMu sync.Mutex
	state   State
	started bool
}

func NewService(store Store) *Service {
	return NewServiceWithListen(store, net.Listen)
}

// NewServiceWithListen is useful for deterministic tests and alternate
// launchers; production callers should use NewService.
func NewServiceWithListen(store Store, listen func(string, string) (net.Listener, error)) *Service {
	if listen == nil {
		listen = net.Listen
	}
	return &Service{store: store, host: DefaultHost, listen: listen}
}

// Bootstrap binds and, on the first run, persists the selected port. An
// explicit override is never written to the settings table.
func (s *Service) Bootstrap(ctx context.Context, override string) (net.Listener, State, error) {
	if s == nil || s.store == nil {
		return nil, State{}, ErrSettingsMissing
	}
	if strings.TrimSpace(override) != "" {
		listener, err := s.bind(strings.TrimSpace(override))
		if err != nil {
			return nil, State{}, err
		}
		port, err := portFromAddr(listener.Addr())
		if err != nil {
			listener.Close()
			return nil, State{}, err
		}
		configured, hasConfigured, err := s.readConfigured(ctx)
		if err != nil {
			listener.Close()
			return nil, State{}, err
		}
		host := hostFromAddr(listener.Addr(), s.host)
		state := makeState(host, port, configured, hasConfigured, true)
		s.setState(state)
		return listener, state, nil
	}

	configured, hasConfigured, err := s.readConfigured(ctx)
	if err != nil {
		return nil, State{}, err
	}
	if hasConfigured {
		listener, err := s.bind(net.JoinHostPort(s.host, strconv.Itoa(configured)))
		if err != nil {
			if errors.Is(err, syscall.EADDRINUSE) {
				return nil, State{}, fmt.Errorf("%w: %s: %v", ErrPortInUse, net.JoinHostPort(s.host, strconv.Itoa(configured)), err)
			}
			return nil, State{}, fmt.Errorf("bind persisted listen port %s: %w", net.JoinHostPort(s.host, strconv.Itoa(configured)), err)
		}
		state := makeState(s.host, configured, configured, true, false)
		s.setState(state)
		return listener, state, nil
	}

	listener, err := s.bindFirstAvailable()
	if err != nil {
		return nil, State{}, err
	}
	port, err := portFromAddr(listener.Addr())
	if err != nil {
		listener.Close()
		return nil, State{}, err
	}
	if err := s.store.Set(ctx, SettingListenPort, strconv.Itoa(port)); err != nil {
		listener.Close()
		return nil, State{}, fmt.Errorf("persist selected listen port: %w", err)
	}
	state := makeState(s.host, port, port, true, false)
	s.setState(state)
	return listener, state, nil
}

func (s *Service) State(ctx context.Context) (State, error) {
	s.mu.RLock()
	started := s.started
	state := s.state
	s.mu.RUnlock()
	if started {
		return state, nil
	}
	configured, hasConfigured, err := s.readConfigured(ctx)
	if err != nil {
		return State{}, err
	}
	return makeState(s.host, 0, configured, hasConfigured, false), nil
}

// SetPort validates and stores a port for the next non-overridden launch. The
// bind check is advisory; the startup bind remains authoritative.
func (s *Service) SetPort(ctx context.Context, port int) (State, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := ValidatePort(port); err != nil {
		return State{}, err
	}
	state, err := s.State(ctx)
	if err != nil {
		return State{}, err
	}
	if port != state.ActivePort || state.ActivePort == 0 {
		listener, listenErr := s.bind(net.JoinHostPort(s.host, strconv.Itoa(port)))
		if listenErr != nil {
			if errors.Is(listenErr, syscall.EADDRINUSE) {
				return State{}, fmt.Errorf("%w: %s: %v", ErrPortInUse, net.JoinHostPort(s.host, strconv.Itoa(port)), listenErr)
			}
			return State{}, fmt.Errorf("check listen port %s: %w", net.JoinHostPort(s.host, strconv.Itoa(port)), listenErr)
		}
		_ = listener.Close()
	}
	if err := s.store.Set(ctx, SettingListenPort, strconv.Itoa(port)); err != nil {
		return State{}, fmt.Errorf("persist listen port: %w", err)
	}
	s.mu.Lock()
	if s.started {
		s.state.ConfiguredPort = port
		s.state.HasConfigured = true
		s.state.RestartRequired = s.state.OverrideActive || s.state.ActivePort != port
		state = s.state
	} else {
		state.ConfiguredPort = port
		state.HasConfigured = true
	}
	s.mu.Unlock()
	return state, nil
}

func ValidatePort(port int) error {
	if port < MinUserPort || port > MaxUserPort {
		return fmt.Errorf("%w: must be between %d and %d", ErrInvalidPort, MinUserPort, MaxUserPort)
	}
	return nil
}

func (s *Service) readConfigured(ctx context.Context) (int, bool, error) {
	value, ok, err := s.store.Get(ctx, SettingListenPort)
	if err != nil {
		return 0, false, fmt.Errorf("read listen port: %w", err)
	}
	if !ok || strings.TrimSpace(value) == "" {
		return 0, false, nil
	}
	port, parseErr := strconv.Atoi(strings.TrimSpace(value))
	if parseErr != nil || ValidatePort(port) != nil {
		return 0, false, fmt.Errorf("%w: %q", ErrInvalidStored, value)
	}
	return port, true, nil
}

func (s *Service) bind(address string) (net.Listener, error) {
	listener, err := s.listen("tcp", address)
	if err != nil {
		return nil, err
	}
	return listener, nil
}

func (s *Service) bindFirstAvailable() (net.Listener, error) {
	var lastErr error
	for port := PreferredPort; port <= CandidateLastPort; port++ {
		listener, err := s.bind(net.JoinHostPort(s.host, strconv.Itoa(port)))
		if err == nil {
			return listener, nil
		}
		lastErr = err
	}
	listener, err := s.bind(net.JoinHostPort(s.host, "0"))
	if err == nil {
		return listener, nil
	}
	if lastErr != nil {
		return nil, fmt.Errorf("select free listen port: candidates exhausted (%v); ephemeral bind failed: %w", lastErr, err)
	}
	return nil, fmt.Errorf("select free listen port: %w", err)
}

func portFromAddr(address net.Addr) (int, error) {
	_, portText, err := net.SplitHostPort(address.String())
	if err != nil {
		return 0, fmt.Errorf("read bound listen port: %w", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port <= 0 || port > MaxUserPort {
		return 0, fmt.Errorf("read bound listen port: %q", portText)
	}
	return port, nil
}

func hostFromAddr(address net.Addr, fallback string) string {
	host, _, err := net.SplitHostPort(address.String())
	if err != nil || host == "" || host == "::" {
		return fallback
	}
	return host
}

func makeState(host string, active, configured int, hasConfigured, override bool) State {
	return State{Host: host, ActivePort: active, ConfiguredPort: configured, HasConfigured: hasConfigured, OverrideActive: override, RestartRequired: override || (active != 0 && configured != active)}
}

func (s *Service) setState(state State) {
	s.mu.Lock()
	s.state = state
	s.started = true
	s.mu.Unlock()
}
