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

