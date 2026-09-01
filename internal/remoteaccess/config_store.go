package remoteaccess

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var hostnamePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

func ValidateConfig(config Config) (Config, error) {
	config.Mode = Mode(strings.ToLower(strings.TrimSpace(string(config.Mode))))
	config.Hostname = strings.ToLower(strings.TrimSpace(config.Hostname))
	if config.Hostname == "" {
		config.Hostname = defaultHost
	}
	switch config.Mode {
	case ModeDisabled, ModePrivate, ModeFunnel:
	default:
		return Config{}, errors.New("remote access mode must be disabled, private, or funnel")
	}
	if len(config.Hostname) > maxHostname || !hostnamePattern.MatchString(config.Hostname) {
		return Config{}, fmt.Errorf("hostname must contain lowercase letters, numbers, and hyphens and be at most %d characters", maxHostname)
	}
	return config, nil
}

func loadConfig(ctx context.Context, store SettingsStore) (Config, *ErrorInfo) {
	config := Config{Mode: ModeDisabled, Hostname: defaultHost}
	value, found, err := store.Get(ctx, configKey)
	if err != nil {
		return config, &ErrorInfo{Code: "config_load_failed", Message: "remote-access settings could not be loaded"}
	}
	if !found || strings.TrimSpace(value) == "" {
		return config, nil
	}
	var decoded Config
	if err := json.Unmarshal([]byte(value), &decoded); err != nil {
		return config, &ErrorInfo{Code: "invalid_config", Message: "saved remote-access settings are invalid"}
	}
	validated, err := ValidateConfig(decoded)
	if err != nil {
		return config, &ErrorInfo{Code: "invalid_config", Message: "saved remote-access settings are invalid"}
	}
	return validated, nil
}

func saveConfig(ctx context.Context, store SettingsStore, config Config) error {
	value, err := json.Marshal(config)
	if err != nil {
		return err
	}
	return store.Set(ctx, configKey, string(value))
}

func saveResult(ctx context.Context, store SettingsStore, status Status) {
	result := struct {
		Phase     Phase      `json:"phase"`
		Error     *ErrorInfo `json:"error,omitempty"`
		UpdatedAt string     `json:"updated_at"`
	}{Phase: status.Phase, Error: status.LastError, UpdatedAt: status.UpdatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")}
	value, err := json.Marshal(result)
	if err == nil {
		_ = store.Set(ctx, lastResultKey, string(value))
	}
}
