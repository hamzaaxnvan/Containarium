package mtls

import (
	"fmt"
	"os"

	certsmem "github.com/footprintai/go-certs/pkg/certs/mem"
	grpccerts "github.com/footprintai/go-certs/pkg/grpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// LoadServerCredentials loads server TLS credentials from certificate files
func LoadServerCredentials(paths CertPaths) (credentials.TransportCredentials, error) {
	// Read certificate files
	caCert, err := os.ReadFile(paths.CACert)
	if err != nil {
		return nil, fmt.Errorf("failed to read CA cert: %w", err)
	}
	clientCert, err := os.ReadFile(paths.ClientCert)
	if err != nil {
		return nil, fmt.Errorf("failed to read client cert: %w", err)
	}
	clientKey, err := os.ReadFile(paths.ClientKey)
	if err != nil {
