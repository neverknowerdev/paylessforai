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
	"github.com/neverknowerdev/paylessforai/internal/db"
	"github.com/neverknowerdev/paylessforai/internal/db/repositories"
	"github.com/neverknowerdev/paylessforai/internal/groups"
	"github.com/neverknowerdev/paylessforai/internal/instance"
	"github.com/neverknowerdev/paylessforai/internal/matcher"
	"github.com/neverknowerdev/paylessforai/internal/network"
	"github.com/neverknowerdev/paylessforai/internal/providers"
	"github.com/neverknowerdev/paylessforai/internal/proxy"
	"github.com/neverknowerdev/paylessforai/internal/secrets"
	"github.com/neverknowerdev/paylessforai/internal/updater"
)

var ErrUpdateRequested = errors.New("restart requested for update")

// Run starts the local app and blocks until it exits or receives a termination
// signal. args are command-line arguments for the app config.
func Run(parent context.Context, args []string) error {
	c, err := config.Parse(args)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(c.DataDir, 0o700); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	processLock, err := instance.Acquire(filepath.Join(c.DataDir, "paylessforai.lock"))
	if err != nil {
		return fmt.Errorf("acquire application lock: %w", err)
	}
	defer processLock.Close()

	db, err := db.Open(parent, filepath.Join(c.DataDir, "paylessforai.db"))
	if err != nil {
		return err
	}
	defer db.Close()

	networkService := network.NewService(db.Settings)
	listenOverride := ""
	if c.ListenExplicit {
		listenOverride = c.ListenAddr
	}
	listener, networkState, err := networkService.Bootstrap(parent, listenOverride)
	if err != nil {
		return fmt.Errorf("resolve HTTP listen port: %w", err)
	}
	defer listener.Close()
	source := "persisted_or_first_run"
	if c.ListenExplicit {
		source = "cli"
	}
	slog.Info("paylessforai HTTP server selected", "address", networkState.ActiveAddress(), "base_url", networkState.BaseURL(), "source", source)

	secretBox, err := secrets.LoadOrCreate(filepath.Join(c.DataDir, "master.key"))
	if err != nil {
		return err
	}
	registry := providers.Builtin(c.ProviderBaseURLs)
	clients := loadProviderClients(registry, db, secretBox)
	catalogManager := catalog.New(clients)
	groupManager := groups.NewManager(db.Groups)
	if err := groupManager.Reload(parent); err != nil {
		slog.Warn("group load failed", "error", err)
	}
	catalogManager.SetRefreshHook(func(ctx context.Context, routes []matcher.Route) error {
		if err := db.Groups.IncludeDiscoveredRoutes(ctx, routes); err != nil {
			return err
		}
		return groupManager.Reload(ctx)
	})
	if stored, err := db.ProviderCredentials.List(parent); err == nil {
		for _, credential := range stored {
			if credential.SubscriptionStatus == "limited" {
				var until *time.Time
				if credential.NextAvailableAt != nil {
					if parsed, parseErr := time.Parse(time.RFC3339Nano, *credential.NextAvailableAt); parseErr == nil {
						until = &parsed
					}
				}
				catalogManager.SetProviderBlocked(credential.ID, until)
			}
		}
	}
	appContext, cancel := context.WithCancel(parent)
	defer cancel()
	updates, err := updater.NewService(c.DataDir, db.Settings, cancel)
	if err != nil {
		return err
	}
	defer updates.Close()
	updates.Start(appContext)
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
	proxyHandler.SetGroups(groupManager)
	server, err := controlplane.NewWithDeps(
		networkState.ActiveAddress(),
		c.ReadHeaderTimeout,
		c.IdleTimeout,
		db,
		catalogManager,
		proxyHandler,
		controlplane.CredentialDeps{Box: secretBox, Registry: registry, Reload: reloadProviders, Updates: updates, Groups: groupManager, Network: networkService},
	)
	if err != nil {
		return fmt.Errorf("create app HTTP server: %w", err)
	}

	if readyPath, token := os.Getenv("PAYLESSFORAI_READY_PATH"), os.Getenv("PAYLESSFORAI_READY_TOKEN"); readyPath != "" && token != "" {
		_ = updater.MarkReady(readyPath, token)
	}
	serverErr := make(chan error, 1)
	go func() { serverErr <- server.Serve(listener) }()
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
	case <-appContext.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), c.ShutdownTimeout)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		if updates.IsUpdateRequested() {
			return ErrUpdateRequested
		}
		return nil
	}
}

// Preflight opens the candidate against a disposable data directory. Opening
// the store exercises the embedded migration set without binding a listener or
// touching provider credentials.
func Preflight(args []string) error {
	c, err := config.Parse(args)
	if err != nil {
		return err
	}
	store, err := db.Open(context.Background(), filepath.Join(c.DataDir, "paylessforai.db"))
	if err != nil {
		return err
	}
	return store.Close()
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

func loadProviderClients(registry *providers.Registry, repos *repositories.Repositories, box *secrets.Box) []providers.Client {
	clients := make([]providers.Client, 0)
	stored, err := repos.ProviderCredentials.List(context.Background())
	if err == nil {
		for _, credential := range stored {
			provider := strings.ToLower(strings.TrimSpace(credential.Provider))
			if !credential.Enabled {
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
			billing := matcher.BillingMetered
			if credential.AccessMode == "subscription" {
				billing = matcher.BillingSubscription
			}
			clients = append(clients, credentialClient{Client: client, id: credential.ID, billing: billing})
		}
	}
	return clients
}

type credentialClient struct {
	providers.Client
	id      string
	billing matcher.BillingClass
}

func (c credentialClient) ExecutionKey() string               { return c.id }
func (c credentialClient) CredentialID() string               { return c.id }
func (c credentialClient) BillingClass() matcher.BillingClass { return c.billing }
