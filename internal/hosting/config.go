package hosting

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	// DefaultConfigDir is the default directory for hosting configuration
	DefaultConfigDir = "/etc/containarium"

	// DefaultConfigFile is the default hosting configuration file
	DefaultConfigFile = "hosting.json"
)

// Config holds the hosting configuration including provider credentials
type Config struct {
	// Provider is the DNS provider name (e.g., "godaddy")
	Provider string `json:"provider"`

	// Domain is the base domain for app hosting
	Domain string `json:"domain"`

	// Email is the email for Let's Encrypt notifications
	Email string `json:"email"`

	// Credentials holds provider-specific credentials
	Credentials ProviderCredentials `json:"credentials"`
}

// ProviderCredentials holds DNS provider API credentials
type ProviderCredentials struct {
	// APIKey is the API key (used by most providers)
	APIKey string `json:"api_key,omitempty"`

	// APISecret is the API secret (used by GoDaddy, Namecheap, etc.)
	APISecret string `json:"api_secret,omitempty"`

	// APIToken is a combined token (used by Cloudflare, DigitalOcean, etc.)
	APIToken string `json:"api_token,omitempty"`
}

// ConfigPath returns the full path to the hosting config file
func ConfigPath() string {
	return filepath.Join(DefaultConfigDir, DefaultConfigFile)
}

// LoadConfig loads the hosting configuration from the default location
func LoadConfig() (*Config, error) {
	return LoadConfigFromFile(ConfigPath())
}

// LoadConfigFromFile loads the hosting configuration from a specific file
func LoadConfigFromFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("hosting config not found at %s (run 'containarium hosting setup' first)", path)
		}
		return nil, fmt.Errorf("read config file: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config file: %w", err)
	}

	return &cfg, nil
}

// SaveConfig saves the hosting configuration to the default location
func SaveConfig(cfg *Config) error {
	return SaveConfigToFile(cfg, ConfigPath())
}

// SaveConfigToFile saves the hosting configuration to a specific file
func SaveConfigToFile(cfg *Config, path string) error {
	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	// Write with restricted permissions (owner read/write only)
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write config file: %w", err)
	}

	return nil
}

// ConfigExists checks if the hosting config file exists
func ConfigExists() bool {
	_, err := os.Stat(ConfigPath())
	return err == nil
}

// DeleteConfig removes the hosting configuration file
func DeleteConfig() error {
	path := ConfigPath()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove config file: %w", err)
	}
	return nil
}
