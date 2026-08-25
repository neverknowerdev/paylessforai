package config

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Config struct {
	DataDir           string
	ListenAddr        string
	ReadHeaderTimeout time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
	RefreshInterval   time.Duration
	ProviderBaseURLs  map[string]string
}

func Default() Config {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		base = "."
	}
	return Config{DataDir: filepath.Join(base, "paylessforai"), ListenAddr: "127.0.0.1:9472", ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute, ShutdownTimeout: 10 * time.Second, RefreshInterval: 5 * time.Minute, ProviderBaseURLs: map[string]string{}}
}

func Parse(args []string) (Config, error) {
	c := Default()
	flags := flag.NewFlagSet("paylessforai", flag.ContinueOnError)
	baseURLs := cloneStrings(c.ProviderBaseURLs)
	openRouterBaseURL := baseURLs["openrouter"]
	surplusBaseURL := baseURLs["surplus"]
	overrides := providerBaseURLsFlag{}
	flags.StringVar(&c.DataDir, "data-dir", c.DataDir, "directory for database and secrets")
	flags.StringVar(&c.ListenAddr, "listen", c.ListenAddr, "HTTP listen address")
	flags.DurationVar(&c.ReadHeaderTimeout, "read-header-timeout", c.ReadHeaderTimeout, "HTTP request-header timeout")
	flags.DurationVar(&c.IdleTimeout, "idle-timeout", c.IdleTimeout, "HTTP keep-alive idle timeout")
	flags.DurationVar(&c.ShutdownTimeout, "shutdown-timeout", c.ShutdownTimeout, "graceful shutdown timeout")
	flags.DurationVar(&c.RefreshInterval, "refresh-interval", c.RefreshInterval, "provider refresh interval")
	flags.StringVar(&openRouterBaseURL, "openrouter-base-url", openRouterBaseURL, "OpenRouter API base URL (compatibility override)")
	flags.StringVar(&surplusBaseURL, "surplus-base-url", surplusBaseURL, "Surplus Intelligence API base URL (compatibility override)")
	flags.Var(&overrides, "provider-base-url", "provider endpoint override in the form name=url; may be repeated")
	if err := flags.Parse(args); err != nil {
		return Config{}, err
	}
	if openRouterBaseURL != "" {
		baseURLs["openrouter"] = openRouterBaseURL
	}
	if surplusBaseURL != "" {
		baseURLs["surplus"] = surplusBaseURL
	}
	for name, url := range overrides {
		baseURLs[name] = url
	}
	c.ProviderBaseURLs = baseURLs
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
	return nil
}

type providerBaseURLsFlag map[string]string

func (f *providerBaseURLsFlag) String() string {
	if f == nil || len(*f) == 0 {
		return ""
	}
	return fmt.Sprintf("%v", map[string]string(*f))
}

func (f *providerBaseURLsFlag) Set(value string) error {
	name, url, ok := strings.Cut(value, "=")
	name, url = strings.ToLower(strings.TrimSpace(name)), strings.TrimSpace(url)
	if !ok || name == "" || url == "" {
		return errors.New("provider-base-url must use name=url")
	}
	if *f == nil {
		*f = make(map[string]string)
	}
	(*f)[name] = url
	return nil
}

func cloneStrings(input map[string]string) map[string]string {
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}
