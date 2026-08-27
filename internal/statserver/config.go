package statserver

import (
	"errors"
	"os"
	"time"
)

type Config struct {
	ListenAddr        string
	AdminListenAddr   string
	DatabaseURL       string
	RefreshInterval   time.Duration
	ArtificialKey     string
	OpenRouterKey     string
	HuggingFaceToken  string
	SurplusKey        string
	BootstrapEmail    string
	BootstrapPassword string
}

func ConfigFromEnv() Config {
	interval := time.Hour
	if v := os.Getenv("STAT_SERVER_REFRESH_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			interval = d
		}
	}
	return Config{ListenAddr: getenvDefault("STAT_SERVER_LISTEN", "127.0.0.1:9580"), AdminListenAddr: getenvDefault("STAT_SERVER_ADMIN_LISTEN", "127.0.0.1:9581"), DatabaseURL: os.Getenv("STAT_SERVER_DATABASE_URL"), RefreshInterval: interval, ArtificialKey: os.Getenv("ARTIFICIAL_ANALYSIS_API_KEY"), OpenRouterKey: os.Getenv("OPENROUTER_API_KEY"), HuggingFaceToken: os.Getenv("HUGGINGFACE_TOKEN"), SurplusKey: os.Getenv("SURPLUS_API_KEY"), BootstrapEmail: os.Getenv("STAT_SERVER_BOOTSTRAP_ADMIN_EMAIL"), BootstrapPassword: os.Getenv("STAT_SERVER_BOOTSTRAP_ADMIN_PASSWORD")}
}

func (c Config) Validate() error {
	if c.DatabaseURL == "" {
		return errors.New("STAT_SERVER_DATABASE_URL is required")
	}
	if c.ListenAddr == "" || c.AdminListenAddr == "" {
		return errors.New("listen addresses are required")
	}
	if c.RefreshInterval <= 0 {
		return errors.New("refresh interval must be positive")
	}
	return nil
}
