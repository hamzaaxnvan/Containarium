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
