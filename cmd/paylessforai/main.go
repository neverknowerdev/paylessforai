package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/neverknowerdev/paylessforai/internal/catalog"
	"github.com/neverknowerdev/paylessforai/internal/config"
	"github.com/neverknowerdev/paylessforai/internal/httpapi"
	"github.com/neverknowerdev/paylessforai/internal/providers"
	"github.com/neverknowerdev/paylessforai/internal/proxy"
	"github.com/neverknowerdev/paylessforai/internal/secrets"
	"github.com/neverknowerdev/paylessforai/internal/store"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		slog.Error("paylessforai stopped", "error", err)
		os.Exit(1)
	}
}

func run(parent context.Context, args []string) error {
	c, err := config.Parse(args)
	if err != nil {
		return err
	}
	s, err := store.Open(parent, filepath.Join(c.DataDir, "paylessforai.db"))
	if err != nil {
		return err
	}
	defer s.Close()
	secretBox, err := secrets.LoadOrCreate(filepath.Join(c.DataDir, "master.key"))
	if err != nil {
		return err
	}
	providerBases := map[string]string{"openrouter": c.OpenRouterBaseURL, "surplus": c.SurplusBaseURL}
	clients := loadProviderClients(c, s, secretBox)
	catalogManager := catalog.New(clients)
	appContext, cancel := context.WithCancel(parent)
	defer cancel()
	if len(clients) > 0 {
		if refreshErr := catalogManager.Refresh(appContext); refreshErr != nil {
			slog.Warn("provider catalog refresh failed", "error", refreshErr)
		}
		go refreshCatalogPeriodically(appContext, catalogManager, c.RefreshInterval)
	}
	reloadProviders := func() error {
		catalogManager.SetClients(loadProviderClients(c, s, secretBox))
		return catalogManager.Refresh(appContext)
	}
	proxyHandler := proxy.New(catalogManager, s)
	server, err := httpapi.NewWithDeps(c.ListenAddr, c.ReadHeaderTimeout, c.IdleTimeout, s, catalogManager, proxyHandler, httpapi.CredentialDeps{Box: secretBox, ProviderBases: providerBases, Reload: reloadProviders})
	if err != nil {
		return fmt.Errorf("create HTTP server: %w", err)
	}
	serverErr := make(chan error, 1)
	go func() { serverErr <- server.ListenAndServe() }()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-serverErr:
		if errors.Is(err, http.ErrServerClosed) || errors.Is(err, context.Canceled) || errors.Is(err, os.ErrClosed) || errors.Is(err, syscall.EINVAL) {
			return nil
		}
		return err
	case <-signals:
		shutdownCtx, cancel := context.WithTimeout(context.Background(), c.ShutdownTimeout)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case <-parent.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), c.ShutdownTimeout)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	}
}

func refreshCatalogPeriodically(ctx context.Context, manager *catalog.Manager, interval time.Duration) {
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := manager.Refresh(ctx); err != nil {
				slog.Warn("provider catalog refresh failed", "error", err)
			}
		case <-ctx.Done():
			return
		}
	}
}

func loadProviderClients(c config.Config, dataStore *store.Store, box *secrets.Box) []providers.Client {
	clients := make([]providers.Client, 0, 4)
	stored, err := dataStore.ListProviderCredentials(context.Background())
	if err == nil {
		for _, credential := range stored {
			if !credential.Enabled {
				continue
			}
			secret, err := box.Open(credential.Ciphertext, credential.Nonce)
			if err != nil {
				continue
			}
			baseURL := c.OpenRouterBaseURL
			if credential.Provider == "surplus" {
				baseURL = c.SurplusBaseURL
			}
			clients = append(clients, providers.NewHTTPClient(credential.Provider, baseURL, secret))
		}
	}
	if c.OpenRouterAPIKey != "" {
		clients = append(clients, providers.NewHTTPClient("openrouter", c.OpenRouterBaseURL, c.OpenRouterAPIKey))
	}
	if c.SurplusAPIKey != "" {
		clients = append(clients, providers.NewHTTPClient("surplus", c.SurplusBaseURL, c.SurplusAPIKey))
	}
	return clients
}
