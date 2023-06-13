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

