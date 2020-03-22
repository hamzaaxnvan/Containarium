package mtls

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/footprintai/go-certs/pkg/certs"
	certsgen "github.com/footprintai/go-certs/pkg/certs/gen"
)

const (
	DefaultCertsDir = "/etc/containarium/certs"
	CAFile          = "ca.crt"
	CAKeyFile       = "ca.key"
	ServerCertFile  = "server.crt"
	ServerKeyFile   = "server.key"
	ClientCertFile  = "client.crt"
	ClientKeyFile   = "client.key"
)

// CertPaths contains paths to all certificate files
type CertPaths struct {
	CACert     string
	CAKey      string
	ServerCert string
	ServerKey  string
	ClientCert string
	ClientKey  string
}

// DefaultCertPaths returns the default certificate paths
func DefaultCertPaths() CertPaths {
	return CertPaths{
		CACert:     filepath.Join(DefaultCertsDir, CAFile),
		CAKey:      filepath.Join(DefaultCertsDir, CAKeyFile),
		ServerCert: filepath.Join(DefaultCertsDir, ServerCertFile),
		ServerKey:  filepath.Join(DefaultCertsDir, ServerKeyFile),
		ClientCert: filepath.Join(DefaultCertsDir, ClientCertFile),
		ClientKey:  filepath.Join(DefaultCertsDir, ClientKeyFile),
	}
}

// CertPathsFromDir returns certificate paths for a specific directory
func CertPathsFromDir(dir string) CertPaths {
	return CertPaths{
		CACert:     filepath.Join(dir, CAFile),
		CAKey:      filepath.Join(dir, CAKeyFile),
		ServerCert: filepath.Join(dir, ServerCertFile),
		ServerKey:  filepath.Join(dir, ServerKeyFile),
		ClientCert: filepath.Join(dir, ClientCertFile),
		ClientKey:  filepath.Join(dir, ClientKeyFile),
	}
}

// GenerateOptions contains options for certificate generation
type GenerateOptions struct {
	// Organization name for certificates
	Organization string
	// Additional DNS names (besides localhost)
	DNSNames []string
	// Additional IP addresses (besides 127.0.0.1)
	IPAddresses []string
	// Certificate validity duration
	Duration time.Duration
	// Output directory for certificates
	OutputDir string
}

// DefaultGenerateOptions returns default certificate generation options
func DefaultGenerateOptions() GenerateOptions {
	return GenerateOptions{
		Organization: "Containarium",
		DNSNames:     []string{"containarium-daemon"},
		IPAddresses:  []string{},
		Duration:     365 * 24 * time.Hour, // 1 year
		OutputDir:    DefaultCertsDir,
	}
}

// Generate creates a new CA and generates server/client certificates
func Generate(opts GenerateOptions) error {
	// Create output directory
	if err := os.MkdirAll(opts.OutputDir, 0700); err != nil {
		return fmt.Errorf("failed to create certs directory: %w", err)
	}

	// Generate credentials
	now := time.Now()
	var credentials *certs.TLSCredentials
	var err error

	// Build options based on what's provided
	if len(opts.DNSNames) > 0 && len(opts.IPAddresses) > 0 {
		credentials, err = certsgen.NewTLSCredentials(
			now,
			now.Add(opts.Duration),
			certsgen.WithOrganizations(opts.Organization),
			certsgen.WithAliasDNSNames(opts.DNSNames...),
			certsgen.WithAliasIPs(opts.IPAddresses...),
		)
	} else if len(opts.DNSNames) > 0 {
		credentials, err = certsgen.NewTLSCredentials(
			now,
			now.Add(opts.Duration),
