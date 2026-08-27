package statserver

// Package statserver contains the complete single-process catalog service.
// Each file owns one responsibility: lifecycle, source ingestion, identity,
// public APIs, telemetry, scoring, authentication, or administration.

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"log"
	"net/http"
	"sync"
	"time"
)

//go:embed migrations/001_init.sql
var migrationSQL string

type Server struct {
	cfg            Config
	db             *sql.DB
	public, admin  *http.Server
	mu             sync.Mutex
	refreshRunning bool
}

func New(cfg Config) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	db, err := openDB(cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	if err = migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	s := &Server{cfg: cfg, db: db}
	if cfg.BootstrapEmail != "" && cfg.BootstrapPassword != "" {
		if err := s.bootstrapAdmin(); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	return s, nil
}

func (s *Server) Close() error {
	if s.public != nil {
		_ = s.public.Close()
	}
	if s.admin != nil {
		_ = s.admin.Close()
	}
	return s.db.Close()
}

func (s *Server) Run(ctx context.Context) error {
	s.public = &http.Server{Addr: s.cfg.ListenAddr, Handler: s.publicMux(), ReadHeaderTimeout: 10 * time.Second}
	s.admin = &http.Server{Addr: s.cfg.AdminListenAddr, Handler: s.adminMux(), ReadHeaderTimeout: 10 * time.Second}
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
	if err := s.Refresh(ctx); err != nil {
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
	if _, err := s.db.ExecContext(ctx, `SELECT pg_advisory_lock(873491)`); err != nil {
		return err
	}
	defer s.db.ExecContext(context.Background(), `SELECT pg_advisory_unlock(873491)`)
	connectors := s.connectors()
	var errs []string
	for _, c := range connectors {
		if c.key == "" && c.name != "huggingface" && c.name != "surplus" {
			continue
		}
		if err := s.runConnector(ctx, c); err != nil {
			errs = append(errs, c.name+": "+err.Error())
		}
	}
	if err := s.computeScores(ctx); err != nil {
		errs = append(errs, "scores: "+err.Error())
	}
	if len(errs) > 0 {
		return errors.New(joinErrors(errs))
	}
	return nil
}
