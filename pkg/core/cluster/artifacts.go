package cluster

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// Pinned artifacts. THIS FILE IS THE REVIEWABLE SURFACE: what a release
// ships into tenant clusters is exactly what is pinned here, golden-
// tested so a bump is a visible one-line diff (same rule as the
// metrics-export allowlist). Checksums are the upstream-published
// sha256sum-amd64.txt values for the release.
//
// Artifacts are fetched onto the HOST cache and pushed into VMs over
// the Incus API — a cluster VM never downloads anything at bootstrap.
// The airgap image preload is a later cold-start optimization (design:
// "What has to change at 10x").
const (
	// K3sVersion is the k3s release every managed cluster runs.
	K3sVersion = "v1.33.4+k3s1"
	// K3sSHA256 is the sha256 of the linux-amd64 `k3s` binary.
	K3sSHA256 = "10da34c350ab8a02e4513a6021046db9e9afecc31bae77419bc6444cbd7b1400"

	// CAImage is the digest-pinned stock cluster-autoscaler image the
	// control-plane VM runs (as a containerd task via `k3s ctr`,
	// OUTSIDE the tenant-visible Kubernetes API — see the design's
	// trust boundaries). Version matches the k3s 1.33 line; digest is
	// registry.k8s.io's manifest-list digest for v1.33.0.
	CAImage = "registry.k8s.io/autoscaling/cluster-autoscaler:v1.33.0@sha256:6ef10d108e0e45ecd883e074682330bbd4a3403e767ad56804800f2f4ee816da"
)

// DefaultArtifactBase is the host-side artifact cache root.
const DefaultArtifactBase = "/var/lib/containarium/artifacts"

func k3sDownloadURL() string {
	return "https://github.com/k3s-io/k3s/releases/download/" + url.PathEscape(K3sVersion) + "/k3s"
}

// K3sPath is where EnsureK3s caches the verified binary.
func K3sPath(base string) string {
	return filepath.Join(base, "k8s", K3sVersion, "k3s")
}

// EnsureK3s returns the path to the pinned, checksum-verified k3s
// binary in the host cache, downloading it first if absent. The
// checksum is verified on EVERY call — a corrupted or tampered cache
// entry fails closed rather than being pushed into a tenant's cluster.
func EnsureK3s(ctx context.Context, base string) (string, error) {
	path := K3sPath(base)
	if err := verifySHA256(path, K3sSHA256); err == nil {
		return path, nil
	}
	if err := fetchVerified(ctx, k3sDownloadURL(), path, K3sSHA256); err != nil {
		return "", fmt.Errorf("fetch k3s %s: %w", K3sVersion, err)
