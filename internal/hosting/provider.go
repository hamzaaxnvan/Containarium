// Package hosting provides DNS and SSL setup automation for app hosting.
package hosting

import (
	"context"
	"fmt"

	"github.com/footprintai/containarium/internal/hosting/godaddy"
)

// DNSProvider defines the interface for DNS providers
type DNSProvider interface {
	// Name returns the provider name
	Name() string

	// VerifyCredentials verifies that the credentials are valid
	VerifyCredentials(ctx context.Context) error

	// VerifyDomain verifies that the domain is managed by this provider
	VerifyDomain(ctx context.Context, domain string) error

	// SetupHostingRecords creates the required DNS records for app hosting
	// (main domain and optionally wildcard A records pointing to serverIP)
	SetupHostingRecords(ctx context.Context, domain, serverIP string, includeWildcard bool) error
}

// ProviderConfig holds configuration for a DNS provider
type ProviderConfig struct {
	// Provider name (e.g., "godaddy", "cloudflare")
	Provider string

	// API credentials (provider-specific)
	APIKey    string
	APISecret string

	// Optional API token (some providers use a single token)
	APIToken string
}

// SupportedProviders returns the list of supported DNS providers
func SupportedProviders() []string {
	return []string{"godaddy"}
}

// NewProvider creates a DNS provider based on the configuration
func NewProvider(cfg ProviderConfig) (DNSProvider, error) {
	switch cfg.Provider {
	case "godaddy":
		if cfg.APIKey == "" || cfg.APISecret == "" {
			return nil, fmt.Errorf("godaddy provider requires api-key and api-secret")
		}
		return &godaddyProvider{
			client: godaddy.NewClient(cfg.APIKey, cfg.APISecret),
		}, nil

	// Future providers can be added here:
	// case "cloudflare":
	//     return newCloudflareProvider(cfg)

	default:
		return nil, fmt.Errorf("unsupported DNS provider: %s (supported: %v)", cfg.Provider, SupportedProviders())
	}
}

// godaddyProvider wraps the GoDaddy client to implement DNSProvider
type godaddyProvider struct {
	client *godaddy.Client
}

func (p *godaddyProvider) Name() string {
	return "godaddy"
}

func (p *godaddyProvider) VerifyCredentials(ctx context.Context) error {
	return p.client.VerifyCredentials(ctx)
}

func (p *godaddyProvider) VerifyDomain(ctx context.Context, domain string) error {
	_, err := p.client.GetDomain(ctx, domain)
	return err
}

func (p *godaddyProvider) SetupHostingRecords(ctx context.Context, domain, serverIP string, includeWildcard bool) error {
	return p.client.SetupHostingRecords(ctx, domain, serverIP, includeWildcard)
}
