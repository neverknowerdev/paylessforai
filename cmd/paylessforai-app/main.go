package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/neverknowerdev/paylessforai/app/localproxy"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		slog.Error("paylessforai-app stopped", "error", err)
		os.Exit(1)
	}
}

func run(parent context.Context, args []string) error {
	cfg := localproxy.DefaultConfig()
	flags := flag.NewFlagSet("paylessforai-app", flag.ContinueOnError)
	flags.StringVar(&cfg.ListenAddr, "listen", cfg.ListenAddr, "local proxy listen address")
	flags.StringVar(&cfg.RemoteURL, "server-url", os.Getenv("PAYLESS_SERVER_URL"), "hosted PayLessForAI server URL")
	flags.StringVar(&cfg.ServerAPIKey, "server-api-key", os.Getenv("PAYLESS_SERVER_API_KEY"), "hosted server client key")
	flags.DurationVar(&cfg.ReadHeaderTimeout, "read-header-timeout", cfg.ReadHeaderTimeout, "HTTP request-header timeout")
	flags.DurationVar(&cfg.IdleTimeout, "idle-timeout", cfg.IdleTimeout, "HTTP keep-alive idle timeout")
	if err := flags.Parse(args); err != nil {
		return err
	}
	server, err := localproxy.NewServer(cfg)
	if err != nil {
		return err
	}
	serverErr := make(chan error, 1)
	go func() { serverErr <- server.ListenAndServe() }()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	select {
	case err := <-serverErr:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	case <-signals:
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case <-parent.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	}
}
