package config

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

type Config struct {
	DataDir           string
	ListenAddr        string
	ReadHeaderTimeout time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
	RefreshInterval   time.Duration
	OpenRouterBaseURL string
	OpenRouterAPIKey  string
	SurplusBaseURL    string
	SurplusAPIKey     string
}

func Default() Config {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		base = "."
	}
	return Config{DataDir: filepath.Join(base, "paylessforai"), ListenAddr: "127.0.0.1:9472", ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute, ShutdownTimeout: 10 * time.Second, RefreshInterval: 5 * time.Minute, OpenRouterBaseURL: "https://openrouter.ai/api/v1", OpenRouterAPIKey: os.Getenv("PAYLESS_OPENROUTER_API_KEY"), SurplusBaseURL: "https://api.surplusintelligence.ai/v1", SurplusAPIKey: os.Getenv("PAYLESS_SURPLUS_API_KEY")}
}

func Parse(args []string) (Config, error) {
	c := Default()
	flags := flag.NewFlagSet("paylessforai", flag.ContinueOnError)
	flags.StringVar(&c.DataDir, "data-dir", c.DataDir, "directory for database and secrets")
	flags.StringVar(&c.ListenAddr, "listen", c.ListenAddr, "HTTP listen address")
	flags.DurationVar(&c.ReadHeaderTimeout, "read-header-timeout", c.ReadHeaderTimeout, "HTTP request-header timeout")
	flags.DurationVar(&c.IdleTimeout, "idle-timeout", c.IdleTimeout, "HTTP keep-alive idle timeout")
	flags.DurationVar(&c.ShutdownTimeout, "shutdown-timeout", c.ShutdownTimeout, "graceful shutdown timeout")
	flags.DurationVar(&c.RefreshInterval, "refresh-interval", c.RefreshInterval, "provider refresh interval")
	flags.StringVar(&c.OpenRouterBaseURL, "openrouter-base-url", c.OpenRouterBaseURL, "OpenRouter API base URL")
	flags.StringVar(&c.SurplusBaseURL, "surplus-base-url", c.SurplusBaseURL, "Surplus Intelligence API base URL")
	if err := flags.Parse(args); err != nil {
		return Config{}, err
	}
	return c, c.Validate()
}

func (c Config) Validate() error {
	if c.DataDir == "" {
		return errors.New("data directory is required")
	}
	if c.ListenAddr == "" {
		return errors.New("listen address is required")
	}
	if _, _, err := net.SplitHostPort(c.ListenAddr); err != nil {
		return fmt.Errorf("invalid listen address: %w", err)
	}
	if c.ReadHeaderTimeout <= 0 || c.IdleTimeout <= 0 || c.ShutdownTimeout <= 0 || c.RefreshInterval <= 0 {
		return errors.New("timeouts and refresh interval must be positive")
	}
	if c.OpenRouterBaseURL == "" && c.SurplusBaseURL == "" {
		return errors.New("at least one provider base URL is required")
	}
	return nil
}
