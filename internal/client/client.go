package client

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/footprintai/containarium/internal/mtls"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// ErrNoServerConfigured is what NewGRPCClient/NewHTTPClient return when
// handed an empty server address, instead of attempting to dial it. This is
// the single seam a remote or hybrid command in the containarium client
// passes through when --server, CONTAINARIUM_SERVER, and credentials.json's
// default_server all resolved to nothing — see
// docs/architecture/cli-client-server-split.md ("Server resolution in the
// client"). Offline commands (cert generate, token inspect, version, …)
// never construct a client, so they never hit this — no pre-run failure or
// exemption list needed.
var ErrNoServerConfigured = errors.New("no server configured — run `containarium login` or pass --server")

const (
	// DefaultUserCertsDir is the default directory for user certificates
	DefaultUserCertsDir = "~/.config/containarium/certs"
)

// ClientOptions contains options for creating a gRPC client
type ClientOptions struct {
	// Server address (host:port)
	ServerAddress string
	// Enable mTLS
	UseMTLS bool
	// Certificate directory (defaults to ~/.config/containarium/certs)
	CertsDir string
}

// DefaultClientOptions returns default client options for local insecure connection
func DefaultClientOptions() ClientOptions {
	return ClientOptions{
		ServerAddress: "localhost:50051",
		UseMTLS:       false,
		CertsDir:      expandPath(DefaultUserCertsDir),
	}
}

// NewClient creates a new gRPC client connection to the Containarium daemon
func NewClient(opts ClientOptions) (pb.ContainerServiceClient, *grpc.ClientConn, error) {
	var dialOpts []grpc.DialOption

	if opts.UseMTLS {
		// Load client certificates for mTLS
		certPaths := mtls.CertPathsFromDir(opts.CertsDir)

		// Check if certificates exist
		if !mtls.CertsExist(certPaths) {
			return nil, nil, fmt.Errorf("client certificates not found in %s. Generate them first with: containarium cert generate", opts.CertsDir)
		}

		// Load client dial options with mTLS
