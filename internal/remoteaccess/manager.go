package remoteaccess

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var ErrActive = errors.New("remote access is active")
var ErrShutdown = errors.New("remote access manager is shut down")

type Option func(*Manager)

func WithNodeFactory(factory NodeFactory) Option { return func(m *Manager) { m.factory = factory } }
func WithPollInterval(interval time.Duration) Option {
	return func(m *Manager) {
		if interval > 0 {
			m.pollInterval = interval
		}
	}
}

type Manager struct {
	store          SettingsStore
	stateDir       string
	privateFactory PrivateHandlerFactory
	publicHandler  http.Handler
	factory        NodeFactory
	pollInterval   time.Duration

	mu          sync.RWMutex
	desired     Config
	status      Status
	generation  uint64
	cancel      context.CancelFunc
	done        chan struct{}
	node        Node
	nodeHost    string
	nodeStarted bool
	closed      bool
}

func New(store SettingsStore, dataDir string, privateFactory PrivateHandlerFactory, publicHandler http.Handler, options ...Option) (*Manager, error) {
	if store == nil {
		return nil, errors.New("remote access settings store is required")
	}
	if privateFactory == nil || publicHandler == nil {
		return nil, errors.New("remote access handlers are required")
	}
	config, configErr := loadConfig(context.Background(), store)
	if err := os.MkdirAll(filepath.Join(dataDir, "tailscale"), 0o700); err != nil {
		return nil, fmt.Errorf("create Tailscale state directory: %w", err)
	}
	stateDir := filepath.Join(dataDir, "tailscale")
	_ = os.Chmod(stateDir, 0o700)
	m := &Manager{
		store: store, stateDir: stateDir, privateFactory: privateFactory, publicHandler: publicHandler,
		factory: productionNodeFactory, pollInterval: 500 * time.Millisecond,
		desired: config,
		status:  Status{DesiredMode: config.Mode, EffectiveMode: ModeDisabled, Phase: PhaseDisabled, Hostname: config.Hostname, UpdatedAt: time.Now().UTC()},
	}
	for _, option := range options {
		option(m)
	}
	if configErr != nil {
		m.status.Phase = PhaseError
		m.status.LastError = configErr
		saveResult(context.Background(), store, m.status)
	}
	return m, nil
}

// Start reconciles persisted configuration asynchronously. Disabled is a
// no-op, which keeps local application startup independent of Tailscale.
func (m *Manager) Start(ctx context.Context) {
	m.mu.RLock()
	config := m.desired
	m.mu.RUnlock()
	if config.Mode != ModeDisabled {
		m.begin(ctx, false)
	}
}

func (m *Manager) Status(context.Context) Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	status := m.status
	if status.Action != nil {
		action := *status.Action
		status.Action = &action
	}
	if status.LastError != nil {
		lastError := *status.LastError
		status.LastError = &lastError
	}
	return status
}

func (m *Manager) Configure(ctx context.Context, config Config) error {
	validated, err := ValidateConfig(config)
	if err != nil {
		return err
	}
	m.mu.RLock()
	closed := m.closed
	unchanged := m.desired == validated && m.status.Phase != PhaseError && m.status.Phase != PhaseDisabled
	m.mu.RUnlock()
	if closed {
		return ErrShutdown
	}
	if err := saveConfig(ctx, m.store, validated); err != nil {
		return fmt.Errorf("save remote-access settings: %w", err)
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return ErrShutdown
	}
	if unchanged && m.desired == validated {
		m.mu.Unlock()
		return nil
	}
	m.desired = validated
	m.mu.Unlock()
	if validated.Mode == ModeDisabled {
		m.begin(context.Background(), true)
	} else {
		m.begin(context.Background(), false)
	}
	return nil
}

func (m *Manager) Retry(ctx context.Context) error {
	m.mu.RLock()
	phase, mode, closed := m.status.Phase, m.desired.Mode, m.closed
	m.mu.RUnlock()
	if closed {
		return ErrShutdown
	}
	if mode == ModeDisabled {
		return nil
	}
	if phase == PhaseStarting || phase == PhaseConnecting || phase == PhaseAuthRequired || phase == PhaseOnline || phase == PhaseStopping {
		return nil
	}
	m.begin(context.Background(), false)
	return nil
}

func (m *Manager) Stop(ctx context.Context) error {
	m.mu.RLock()
	config := m.desired
	m.mu.RUnlock()
	config.Mode = ModeDisabled
	return m.Configure(ctx, config)
}

func (m *Manager) ForgetIdentity(ctx context.Context) error {
	m.mu.RLock()
	active := m.activeLocked()
	closed := m.closed
	m.mu.RUnlock()
	if closed {
		return ErrShutdown
	}
	if active {
		return ErrActive
	}
	if err := os.RemoveAll(m.stateDir); err != nil {
		return fmt.Errorf("forget Tailscale identity: %w", err)
	}
	if err := os.MkdirAll(m.stateDir, 0o700); err != nil {
		return err
	}
	_ = os.Chmod(m.stateDir, 0o700)
	saveResult(ctx, m.store, m.Status(context.Background()))
	return nil
}

func (m *Manager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	if !m.closed {
		m.closed = true
		if m.cancel != nil {
			m.cancel()
		}
	}
	done := m.done
	m.mu.Unlock()
	if done == nil {
		m.stopNode()
		return nil
	}
	select {
	case <-done:
		m.stopNode()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager) begin(parent context.Context, disabling bool) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	if m.cancel != nil {
		m.cancel()
	}
	previousDone := m.done
	m.generation++
	generation := m.generation
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	m.cancel, m.done = cancel, done
	if disabling {
		m.status = Status{DesiredMode: ModeDisabled, EffectiveMode: ModeDisabled, Phase: PhaseStopping, Hostname: m.desired.Hostname, UpdatedAt: time.Now().UTC()}
	} else {
		m.status = Status{DesiredMode: m.desired.Mode, EffectiveMode: ModeDisabled, Phase: PhaseStarting, Hostname: m.desired.Hostname, UpdatedAt: time.Now().UTC()}
	}
	status := m.status
	m.mu.Unlock()
	saveResult(context.Background(), m.store, status)
	go func() {
		if previousDone != nil {
			<-previousDone
		}
		m.reconcile(ctx, generation, done)
	}()
}

func (m *Manager) reconcile(ctx context.Context, generation uint64, done chan struct{}) {
	defer close(done)
	defer func() {
		m.mu.Lock()
		if m.generation == generation {
			m.cancel = nil
			m.done = nil
		}
		m.mu.Unlock()
	}()

	m.mu.RLock()
	config := m.desired
	m.mu.RUnlock()
	if config.Mode == ModeDisabled {
		m.stopNode()
		m.publish(generation, Status{DesiredMode: ModeDisabled, EffectiveMode: ModeDisabled, Phase: PhaseDisabled, Hostname: config.Hostname, UpdatedAt: time.Now().UTC()})
		return
	}
	node, err := m.ensureNode(config)
	if err != nil {
		m.fail(generation, config, "node_unavailable", "Tailscale node could not be created")
		return
	}
	for {
		if ctx.Err() != nil {
			return
		}
		state, err := node.Status(ctx)
		if err != nil {
			m.fail(generation, config, "status_failed", "Tailscale status could not be read")
			return
		}
		if !state.Running {
			status := Status{DesiredMode: config.Mode, EffectiveMode: ModeDisabled, Phase: PhaseConnecting, Hostname: config.Hostname, UpdatedAt: time.Now().UTC()}
			if state.AuthURL != "" {
				status.Phase = PhaseAuthRequired
				status.Action = safeAuthAction(state.AuthURL)
			}
			m.publish(generation, status)
			if !wait(ctx, m.pollInterval) {
				return
			}
			continue
		}
		if state.DNSName == "" || state.OwnerID == "" {
			m.fail(generation, config, "identity_unavailable", "Tailscale identity is not available")
			return
		}
		resolver := nodeResolver{node: node}
		private := m.privateFactory(OwnerAuthorizer(resolver, func() string {
			current, err := node.Status(context.Background())
			if err != nil || current == nil || !current.Running {
				return ""
			}
			return current.OwnerID
		}))
		privateLn, err := node.ListenTLS("tcp", ":443")
		if err != nil {
			m.fail(generation, config, "private_listener_failed", "private Tailscale listener could not start")
			return
		}
		listeners := []net.Listener{privateLn}
		servers := []*http.Server{{Handler: private}}
		if config.Mode == ModeFunnel {
			publicLn, publicErr := node.ListenFunnel("tcp", ":443")
			if publicErr != nil {
				_ = privateLn.Close()
				m.fail(generation, config, "funnel_listener_failed", fmt.Sprintf("public Funnel is unavailable: %v", publicErr))
				return
			}
			listeners = append(listeners, publicLn)
			servers = append(servers, &http.Server{Handler: m.publicHandler})
		}
		for i := range servers {
			go func(server *http.Server, listener net.Listener) {
				if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) && !errors.Is(serveErr, net.ErrClosed) {
					// The lifecycle owner records start/stop failures. Serve errors
					// after online are intentionally not surfaced with raw details.
				}
			}(servers[i], listeners[i])
		}
		dnsName := strings.TrimSuffix(state.DNSName, ".")
		m.publish(generation, Status{DesiredMode: config.Mode, EffectiveMode: config.Mode, Phase: PhaseOnline, Hostname: config.Hostname, DNSName: dnsName, DashboardURL: "https://" + dnsName + "/", BaseURL: "https://" + dnsName + "/v1", UpdatedAt: time.Now().UTC()})
		<-ctx.Done()
		for _, server := range servers {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_ = server.Shutdown(shutdownCtx)
			cancel()
		}
		for _, listener := range listeners {
			_ = listener.Close()
		}
		return
	}
}

func (m *Manager) ensureNode(config Config) (Node, error) {
	var old Node
	m.mu.Lock()
	if m.node != nil && m.nodeHost != config.Hostname {
		old = m.node
		m.node = nil
		m.nodeHost = ""
		m.nodeStarted = false
	}
	node := m.node
	started := m.nodeStarted
	if node == nil {
		node = m.factory(config.Hostname, m.stateDir)
		if node != nil {
			m.node = node
			m.nodeHost = config.Hostname
			m.nodeStarted = false
		}
	}
	if node != nil && !started {
		m.nodeStarted = true
	}
	m.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	if node == nil {
		return nil, errors.New("Tailscale node could not be created")
	}
	if !started {
		if err := node.Start(); err != nil {
			m.discardNode(node)
			return nil, fmt.Errorf("Tailscale node could not start: %w", err)
		}
	}
	return node, nil
}

func (m *Manager) discardNode(node Node) {
	m.mu.Lock()
	if m.node == node {
		m.node = nil
		m.nodeHost = ""
		m.nodeStarted = false
	}
	m.mu.Unlock()
	_ = node.Close()
}

func (m *Manager) stopNode() {
	m.mu.Lock()
	node := m.node
	m.node = nil
	m.nodeHost = ""
	m.nodeStarted = false
	m.mu.Unlock()
	if node != nil {
		_ = node.Close()
	}
}

func (m *Manager) publish(generation uint64, status Status) {
	m.mu.Lock()
	if m.generation != generation {
		m.mu.Unlock()
		return
	}
	m.status = status
	m.mu.Unlock()
	saveResult(context.Background(), m.store, status)
}

func (m *Manager) fail(generation uint64, config Config, code, message string) {
	status := Status{DesiredMode: config.Mode, EffectiveMode: ModeDisabled, Phase: PhaseError, Hostname: config.Hostname, LastError: &ErrorInfo{Code: code, Message: message, At: time.Now().UTC()}, UpdatedAt: time.Now().UTC()}
	m.publish(generation, status)
}

func (m *Manager) activeLocked() bool {
	return m.node != nil || m.done != nil || m.status.Phase == PhaseOnline || m.status.Phase == PhaseStarting || m.status.Phase == PhaseConnecting || m.status.Phase == PhaseAuthRequired
}

func wait(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func safeAuthAction(raw string) *Action {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Host == "" {
		return nil
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "tailscale.com" && !strings.HasSuffix(host, ".tailscale.com") {
		return nil
	}
	return &Action{Kind: "authorize_node", Label: "Authorize with Tailscale", URL: parsed.String()}
}

type nodeResolver struct{ node Node }

func (r nodeResolver) WhoIs(ctx context.Context, remoteAddr string) (string, error) {
	return r.node.WhoIs(ctx, remoteAddr)
}

func decodeLastResult(value string) (Phase, *ErrorInfo) {
	var result struct {
		Phase Phase      `json:"phase"`
		Error *ErrorInfo `json:"error"`
	}
	if json.Unmarshal([]byte(value), &result) != nil {
		return "", nil
	}
	return result.Phase, result.Error
}
