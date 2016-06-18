//go:build !containarium_client

package server

// mTLS transport tests for the CA-provider surface (#1415, option 1):
// the client-certificate CN IS the cluster identity. These dial the
// real listener with real minted certs — the exact handshake the
// stock CA binary performs.

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	clustercore "github.com/footprintai/containarium/pkg/core/cluster"
	capb "github.com/footprintai/containarium/pkg/pb/thirdparty/externalgrpc"
)

func mtlsRig(t *testing.T) (*ClusterPKI, string, *ClusterServer, *ClusterReconciler) {
	t.Helper()
	pki, err := LoadOrCreateClusterPKI(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv, rec, host := testReconcilerRig(t)
	mgr := clustercore.NewManagerWithLoader(host, func() ([]byte, error) { return []byte("k3s-bin"), nil })
	provider := NewCAProviderServer(srv.Store(), mgr)
	addr, stop, err := StartClusterCAListener("127.0.0.1:0", []string{"127.0.0.1"}, pki, provider)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	return pki, addr, srv, rec
}

func dialWithCert(t *testing.T, pki *ClusterPKI, addr string, certPEM, keyPEM []byte) capb.CloudProviderClient {
	t.Helper()
	clientCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pki.CAPEM()) {
		t.Fatal("bad CA PEM")
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{clientCert},
		RootCAs:      pool,
	})))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return capb.NewCloudProviderClient(conn)
}

func TestCAMTLS_CertCNIsTheIdentity(t *testing.T) {
	pki, addr, srv, _ := mtlsRig(t)
	mustCreate(t, srv, tenantCtx("alice"), "demo")

	cert, key, err := pki.MintClientCert("alice", "demo")
	if err != nil {
		t.Fatal(err)
	}
	client := dialWithCert(t, pki, addr, cert, key)

	// No bearer token, no metadata — the certificate is the identity.
	resp, err := client.NodeGroups(context.Background(), &capb.NodeGroupsRequest{})
	if err != nil {
		t.Fatalf("NodeGroups over mTLS: %v", err)
	}
	if len(resp.NodeGroups) != 3 || !strings.HasPrefix(resp.NodeGroups[0].Id, "alice/demo/") {
		t.Fatalf("groups over mTLS = %+v", resp.NodeGroups)
	}
}

func TestCAMTLS_WrongClusterCertIsRefused(t *testing.T) {
	pki, addr, srv, _ := mtlsRig(t)
	mustCreate(t, srv, tenantCtx("alice"), "demo")

	// A valid cert for a DIFFERENT cluster passes the handshake but
	// cannot touch demo's node groups.
	cert, key, err := pki.MintClientCert("alice", "other")
	if err != nil {
		t.Fatal(err)
	}
	client := dialWithCert(t, pki, addr, cert, key)
