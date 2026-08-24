package controllers

import (
	"context"
	"net/http"
	"time"

	"github.com/neverknowerdev/paylessforai/internal/catalog"
	proxyservice "github.com/neverknowerdev/paylessforai/internal/proxy"
	"github.com/neverknowerdev/paylessforai/internal/secrets"
	"github.com/neverknowerdev/paylessforai/internal/store"
	"github.com/neverknowerdev/paylessforai/internal/web"
)

type Server struct {
	httpServer  *http.Server
	db          *store.Store
	catalog     *catalog.Manager
	proxy       *proxyservice.Proxy
	credentials CredentialDeps
}

type CredentialDeps struct {
	Box           *secrets.Box
	ProviderBases map[string]string
	Reload        func() error
}

func New(addr string, readHeaderTimeout, idleTimeout time.Duration, db *store.Store) (*Server, error) {
	return NewWithDeps(addr, readHeaderTimeout, idleTimeout, db, nil, nil)
}

func NewWithDeps(addr string, readHeaderTimeout, idleTimeout time.Duration, db *store.Store, catalogManager *catalog.Manager, proxyHandler *proxyservice.Proxy, credentialConfig ...CredentialDeps) (*Server, error) {
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
	mux.Handle("/", ui)
	server.registerModelRoutes(mux)
	server.registerProxyRoutes(mux)
	server.httpServer = &http.Server{Addr: addr, Handler: withRequestID(mux), ReadHeaderTimeout: readHeaderTimeout, IdleTimeout: idleTimeout}
	return server, nil
}

func (s *Server) ListenAndServe() error { return s.httpServer.ListenAndServe() }

func (s *Server) Shutdown(ctx context.Context) error { return s.httpServer.Shutdown(ctx) }
