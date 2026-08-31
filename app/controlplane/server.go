package controlplane

import (
	"context"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/neverknowerdev/paylessforai/app/gateway"
	"github.com/neverknowerdev/paylessforai/internal/catalog"
	"github.com/neverknowerdev/paylessforai/internal/db"
	"github.com/neverknowerdev/paylessforai/internal/providers"
	proxyservice "github.com/neverknowerdev/paylessforai/internal/proxy"
	"github.com/neverknowerdev/paylessforai/internal/secrets"
	"github.com/neverknowerdev/paylessforai/internal/updater"
	"github.com/neverknowerdev/paylessforai/internal/web"
)

type Server struct {
	httpServer  *http.Server
	db          *db.Store
	catalog     *catalog.Manager
	proxy       *proxyservice.Proxy
	credentials CredentialDeps
}

type CredentialDeps struct {
	Box      *secrets.Box
	Registry *providers.Registry
	Reload   func() error
	Updates  *updater.Service
}

func New(addr string, readHeaderTimeout, idleTimeout time.Duration, db *db.Store) (*Server, error) {
	return NewWithDeps(addr, readHeaderTimeout, idleTimeout, db, nil, nil)
}

func NewWithDeps(addr string, readHeaderTimeout, idleTimeout time.Duration, db *db.Store, catalogManager *catalog.Manager, proxyHandler *proxyservice.Proxy, credentialConfig ...CredentialDeps) (*Server, error) {
	credentials := CredentialDeps{}
	if len(credentialConfig) > 0 {
		credentials = credentialConfig[0]
	}
	ui, err := web.Handler()
	if err != nil {
		return nil, err
	}
	server := &Server{db: db, catalog: catalogManager, proxy: proxyHandler, credentials: credentials}
	mux := http.NewServeMux()
	server.registerHealthRoutes(mux)
	server.registerStatsRoutes(mux)
	server.registerKeyRoutes(mux)
	server.registerProviderRoutes(mux)
	server.registerModelRoutes(mux)
	server.registerUpdateRoutes(mux)
	mux.Handle("/", ui)
	public := gateway.NewHandler(catalogManager, proxyHandler)
	mux.Handle("/v1/", public)
	mux.Handle("/anthropic/v1/messages", public)
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

func (s *Server) ListenAndServe() error { return s.httpServer.ListenAndServe() }

func (s *Server) Start() (net.Listener, error) { return net.Listen("tcp", s.httpServer.Addr) }

func (s *Server) Serve(listener net.Listener) error { return s.httpServer.Serve(listener) }

func (s *Server) Shutdown(ctx context.Context) error { return s.httpServer.Shutdown(ctx) }
