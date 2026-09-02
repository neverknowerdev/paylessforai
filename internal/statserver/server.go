package statserver

// Package statserver assembles the single-binary stat-server application.

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/neverknowerdev/paylessforai/internal/statserver/connectors"
	statdb "github.com/neverknowerdev/paylessforai/internal/statserver/db"
	"github.com/neverknowerdev/paylessforai/internal/statserver/repositories"
	"github.com/neverknowerdev/paylessforai/internal/statserver/services"
	"github.com/neverknowerdev/paylessforai/internal/statserver/transport"
	"github.com/neverknowerdev/paylessforai/internal/statserver/views"
)

type Server struct {
	cfg            Config
	database       *sql.DB
	public, admin  *http.Server
	catalog        *services.CatalogService
	profiles       *services.ProfileService
	mu             sync.Mutex
	refreshRunning bool
}

func New(cfg Config) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	database, err := statdb.Open(context.Background(), cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	if err = statdb.Migrate(context.Background(), database); err != nil {
		_ = database.Close()
		return nil, err
	}
	repos := repositories.New(database)
	catalog := services.NewCatalog(repos, connectors.Default(cfg.ArtificialKey, cfg.OpenRouterKey, cfg.HuggingFaceToken, cfg.SurplusKey))
	profiles := services.NewProfiles(repos)
	auth := services.NewAuth(repos)
	if err := auth.Bootstrap(context.Background(), cfg.BootstrapEmail, cfg.BootstrapPassword); err != nil {
		_ = database.Close()
		return nil, err
	}
	renderer, err := views.New()
	if err != nil {
		_ = database.Close()
		return nil, err
	}
	telemetry := services.NewTelemetry(repos)
	return &Server{
		cfg:      cfg,
		database: database,
		catalog:  catalog,
		profiles: profiles,
		public:   &http.Server{Addr: cfg.ListenAddr, Handler: transport.NewPublic(catalog, telemetry, profiles, renderer).Handler(), ReadHeaderTimeout: 10 * time.Second},
		admin:    &http.Server{Addr: cfg.AdminListenAddr, Handler: transport.NewAdmin(catalog, profiles, auth, renderer).Handler(), ReadHeaderTimeout: 10 * time.Second},
	}, nil
}

func (s *Server) Close() error {
	if s.public != nil {
		_ = s.public.Close()
	}
	if s.admin != nil {
		_ = s.admin.Close()
	}
	return s.database.Close()
}

func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 2)
	go func() {
		log.Printf("public stat-server listening on %s", s.cfg.ListenAddr)
		errCh <- s.public.ListenAndServe()
	}()
	go func() {
		log.Printf("admin stat-server listening on %s", s.cfg.AdminListenAddr)
		errCh <- s.admin.ListenAndServe()
	}()
	go s.scheduler(ctx)
	if s.cfg.SkipInitialRefresh {
		log.Printf("initial refresh skipped by configuration")
	} else if err := s.Refresh(ctx); err != nil {
		log.Printf("initial refresh degraded: %v", err)
	}
	select {
	case <-ctx.Done():
		_ = s.public.Shutdown(context.Background())
		_ = s.admin.Shutdown(context.Background())
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func (s *Server) scheduler(ctx context.Context) {
	ticker := time.NewTicker(s.cfg.RefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.Refresh(ctx); err != nil {
				log.Printf("scheduled refresh degraded: %v", err)
			}
		}
	}
}

func (s *Server) Refresh(ctx context.Context) error {
	s.mu.Lock()
	if s.refreshRunning {
		s.mu.Unlock()
		return nil
	}
	s.refreshRunning = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.refreshRunning = false; s.mu.Unlock() }()
	refreshErr := s.catalog.Refresh(ctx)
	scoreErr := s.profiles.Compute(ctx)
	return errors.Join(refreshErr, scoreErr)
}
