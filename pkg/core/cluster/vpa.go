package cluster

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"time"
)

// Vertical Pod Autoscaler (#1416): upstream VPA shipped into every
// managed cluster via the k3s manifest auto-apply dir. Usage→requests
// conversion happens ONLY here (design: "Who decides what"); tenants
// opt workloads in with VPA objects (updateMode InPlaceOrRecreate for
// in-place resizes, Recreate as the fallback — both stock VPA 1.7
// behavior, no bespoke resizing loop).
const (
	// VPAVersion is the pinned upstream release.
	VPAVersion = "1.7.1"
	// VPAManifestSHA256 pins the embedded, digest-pinned manifest
	// bundle — the reviewable surface (golden-tested like the k3s
	// binary pin): what runs inside tenant clusters changes only as
	// a visible re-vendor diff.
	VPAManifestSHA256 = "a74ea4c0e47561d4fd0bb9003a5e005dd5eb43fe173161b7dd86ff97ffebecd4"
)

//go:embed vpa/manifests.yaml
var vpaManifests []byte

// VPAManifests returns the vendored bundle after re-verifying it
// against the pin — a tampered or drifted embed fails closed instead
// of being applied into a tenant's cluster.
func VPAManifests() ([]byte, error) {
	sum := sha256.Sum256(vpaManifests)
	if got := hex.EncodeToString(sum[:]); got != VPAManifestSHA256 {
		return nil, fmt.Errorf("embedded VPA manifests hash %s does not match the pin %s", got, VPAManifestSHA256)
	}
	return vpaManifests, nil
}

// K3s manifest auto-apply paths for the VPA deployment.
const (
	VPAManifestPath = "/var/lib/rancher/k3s/server/manifests/containarium-vpa.yaml"
	VPACertsPath    = "/var/lib/rancher/k3s/server/manifests/containarium-vpa-certs.yaml"
)

