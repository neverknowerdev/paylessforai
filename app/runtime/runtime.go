// Package runtime contains the local PayLessForAI app lifecycle.
//
// Keeping startup and dependency wiring here means the app binary is a
// very small entrypoint, while tests and future launchers can reuse the same
// lifecycle without duplicating provider setup.
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/neverknowerdev/paylessforai/app/controlplane"
	"github.com/neverknowerdev/paylessforai/internal/catalog"
	"github.com/neverknowerdev/paylessforai/internal/config"
	"github.com/neverknowerdev/paylessforai/internal/providers"
	"github.com/neverknowerdev/paylessforai/internal/proxy"
	"github.com/neverknowerdev/paylessforai/internal/secrets"
	"github.com/neverknowerdev/paylessforai/internal/store"
)

// Run starts the local app and blocks until it exits or receives a termination
// signal. args are command-line arguments for the app config.
func Run(parent context.Context, args []string) error {
	c, err := config.Parse(args)
	if err != nil {
		return err
	}

	db, err := store.Open(parent, filepath.Join(c.DataDir, "paylessforai.db"))
	if err != nil {
		return err
	}
	defer db.Close()

	secretBox, err := secrets.LoadOrCreate(filepath.Join(c.DataDir, "master.key"))
	if err != nil {
		return err
	}
	registry := providers.Builtin(c.ProviderBaseURLs)
	clients := loadProviderClients(registry, db, secretBox)
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
		catalogManager.SetClients(loadProviderClients(registry, db, secretBox))
		return catalogManager.Refresh(appContext)
	}
	proxyHandler := proxy.New(catalogManager, db)
	server, err := controlplane.NewWithDeps(
		c.ListenAddr,
		c.ReadHeaderTimeout,
		c.IdleTimeout,
		db,
		catalogManager,
		proxyHandler,
		controlplane.CredentialDeps{Box: secretBox, Registry: registry, Reload: reloadProviders},
	)
	if err != nil {
		return fmt.Errorf("create app HTTP server: %w", err)
	}

	serverErr := make(chan error, 1)
	go func() { serverErr <- server.ListenAndServe() }()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
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

func loadProviderClients(registry *providers.Registry, dataStore *store.Store, box *secrets.Box) []providers.Client {
	clients := make([]providers.Client, 0)
	configured := make(map[string]bool)
	stored, err := dataStore.ListProviderCredentials(context.Background())
	if err == nil {
		for _, credential := range stored {
			provider := strings.ToLower(strings.TrimSpace(credential.Provider))
			if !credential.Enabled || configured[provider] {
				continue
			}
			secret, err := box.Open(credential.Ciphertext, credential.Nonce)
			if err != nil {
				continue
			}
			client, _, err := registry.Resolve(provider, credential.BaseURL, secret)
			if err != nil {
				continue
			}
			if strings.TrimSpace(credential.ManualModelsJSON) != "" && credential.ManualModelsJSON != "[]" {
				var manual []providers.ManualModel
				if json.Unmarshal([]byte(credential.ManualModelsJSON), &manual) == nil && len(manual) > 0 {
					client = providers.WithManualModels{Client: client, Models: manual}
				}
			}
			clients = append(clients, client)
			configured[provider] = true
		}
	}
	return clients
}
