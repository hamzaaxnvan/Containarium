//go:build !containarium_client

package server

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"

	"github.com/footprintai/containarium/internal/auth"
	capb "github.com/footprintai/containarium/pkg/pb/thirdparty/externalgrpc"
)

// ClusterPKI is the dedicated certificate authority for the
// CA-provider mTLS surface (#1415, transport option 1). The stock
// cluster-autoscaler's externalgrpc client authenticates with client
// certificates ONLY, so each cluster's machine identity is a client
// cert whose CN is the same `k8s-cluster:<owner>/<name>` subject the
// provider server already enforces — an interceptor maps CN to the
// identity metadata, and every handler stays transport-agnostic.
//
// This CA is deliberately separate from the daemon's main mTLS PKI: a
// cluster credential can only ever authenticate to the CA-provider
// listener, never to the operator API.
type ClusterPKI struct {
	caCert *x509.Certificate
	caKey  *ecdsa.PrivateKey
	caPEM  []byte
}

// LoadOrCreateClusterPKI loads the cluster CA from dir, creating and
// persisting it (0600 key) on first use.
func LoadOrCreateClusterPKI(dir string) (*ClusterPKI, error) {
	certPath, keyPath := filepath.Join(dir, "ca.crt"), filepath.Join(dir, "ca.key")
	certPEM, cErr := os.ReadFile(certPath)
	keyPEM, kErr := os.ReadFile(keyPath)
	if cErr == nil && kErr == nil {
		return parseClusterPKI(certPEM, keyPEM)
	}

