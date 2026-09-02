package controlplane

import (
	"context"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/neverknowerdev/paylessforai/app/gateway"
	"github.com/neverknowerdev/paylessforai/internal/catalog"
	"github.com/neverknowerdev/paylessforai/internal/clientauth"
	"github.com/neverknowerdev/paylessforai/internal/db/repositories"
	"github.com/neverknowerdev/paylessforai/internal/groups"
	"github.com/neverknowerdev/paylessforai/internal/network"
	"github.com/neverknowerdev/paylessforai/internal/providers"
	proxyservice "github.com/neverknowerdev/paylessforai/internal/proxy"
	"github.com/neverknowerdev/paylessforai/internal/remoteaccess"
	"github.com/neverknowerdev/paylessforai/internal/secrets"
	"github.com/neverknowerdev/paylessforai/internal/updater"
	"github.com/neverknowerdev/paylessforai/internal/web"
)

type Server struct {
	httpServer  *http.Server
	db          *repositories.Repositories
	catalog     *catalog.Manager
	proxy       *proxyservice.Proxy
	credentials CredentialDeps
	groups      *groups.Manager
	control     http.Handler
	controlMux  *http.ServeMux
	gateway     http.Handler
	remote      remoteaccess.Controller
	remoteAdded bool
	network     *network.Service
}

type CredentialDeps struct {
	Box      *secrets.Box
	Registry *providers.Registry
	Reload   func() error
	Updates  *updater.Service
	Groups   *groups.Manager
	Network  *network.Service
}

func New(addr string, readHeaderTimeout, idleTimeout time.Duration, db *repositories.Repositories) (*Server, error) {
	return NewWithDeps(addr, readHeaderTimeout, idleTimeout, db, nil, nil)
}

func NewWithDeps(addr string, readHeaderTimeout, idleTimeout time.Duration, db *repositories.Repositories, catalogManager *catalog.Manager, proxyHandler *proxyservice.Proxy, credentialConfig ...CredentialDeps) (*Server, error) {
	credentials := CredentialDeps{}
	if len(credentialConfig) > 0 {
		credentials = credentialConfig[0]
	}
	ui, err := web.Handler()
	if err != nil {
		return nil, err
	}
	groupManager := credentials.Groups
	if groupManager == nil && db != nil {
		groupManager = groups.NewManager(db.Groups)
		_ = groupManager.Reload(context.Background())
	}
	server := &Server{db: db, catalog: catalogManager, proxy: proxyHandler, credentials: credentials, groups: groupManager, network: credentials.Network}
	controlMux := http.NewServeMux()
	server.registerHealthRoutes(controlMux)
	server.registerStatsRoutes(controlMux)
	server.registerKeyRoutes(controlMux)
	server.registerProviderRoutes(controlMux)
	server.registerModelRoutes(controlMux)
	server.registerUpdateRoutes(controlMux)
	server.registerGroupRoutes(controlMux)
	server.registerSettingsRoutes(controlMux)
	controlMux.Handle("/", ui)
	server.control = withRequestID(controlMux)
	server.controlMux = controlMux
	server.gateway = gateway.NewHandler(catalogManager, proxyHandler, clientauth.Middleware(dbClientKeys(db)), server.groups)
	mux := http.NewServeMux()
	mux.Handle("/", server.control)
	mux.Handle("/v1/", server.gateway)
	mux.Handle("/anthropic/v1/messages", server.gateway)
	var handler http.Handler = withRequestID(mux)
	if gatePath := os.Getenv("PAYLESSFORAI_GATE_PATH"); gatePath != "" {
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/healthz" && r.URL.Path != "/readyz" {
				if _, err := os.Stat(gatePath); err != nil {
					writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "update_in_progress", "message": "application is starting"})
					return
				}
			}
			withRequestID(mux).ServeHTTP(w, r)
		})
	}
	server.httpServer = &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: readHeaderTimeout, IdleTimeout: idleTimeout}
	return server, nil
}

func dbClientKeys(db *repositories.Repositories) clientauth.Authenticator {
	if db == nil {
		return nil
	}
	return db.ClientAPIKeys
}

func (s *Server) SetGroups(manager *groups.Manager) { s.groups = manager }

func (s *Server) GatewayHandler() http.Handler { return s.gateway }

func (s *Server) PrivateHandler(authorize remoteaccess.Authorizer) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", authorize(remoteaccess.ProtectManagement(s.control)))
	mux.Handle("/v1/", s.gateway)
	mux.Handle("/anthropic/v1/messages", s.gateway)
	return withRequestID(mux)
}

func (s *Server) SetRemoteAccess(controller remoteaccess.Controller) {
	s.remote = controller
	if s.remoteAdded || s.control == nil {
		return
	}
	// The control handler is backed by a ServeMux, so adding these routes here
	// is safe before any listener is served and keeps remote control available
	// on loopback and the owner-authorized private listener only.
	if s.controlMux != nil {
		s.registerRemoteAccessRoutes(s.controlMux)
		s.remoteAdded = true
	}
}

func (s *Server) ListenAndServe() error { return s.httpServer.ListenAndServe() }

func (s *Server) Start() (net.Listener, error) { return net.Listen("tcp", s.httpServer.Addr) }

func (s *Server) Serve(listener net.Listener) error { return s.httpServer.Serve(listener) }

func (s *Server) Shutdown(ctx context.Context) error { return s.httpServer.Shutdown(ctx) }
