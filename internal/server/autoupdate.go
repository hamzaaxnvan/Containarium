package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// AutoUpdater periodically checks the sentinel for a newer binary and
// self-updates if a new version is available.
type AutoUpdater struct {
	sentinelURL       string // e.g. "http://10.130.0.13:8888"
	binaryPath        string // e.g. "/usr/local/bin/containariumd" (#1779)
	interval          time.Duration
	watchdogHealthURL string // polled by upgrade-watchdog to confirm liveness (#507); defaults to http://localhost:8080/health

	// Short-lived cache of the sentinel-served version (#2171). Callers such
	// as a control plane rendering a host page hit GetLatestRelease often, and
	// each sentinel lookup checksums the served binary.
	servedMu      sync.Mutex
	servedVersion string
	servedAt      time.Time
}

// servedVersionTTL bounds how stale ServedVersion may be. A fetch-release on
// the sentinel shows up within this window.
const servedVersionTTL = time.Minute

// NewAutoUpdater creates a new auto-updater.
func NewAutoUpdater(sentinelURL, binaryPath string, interval time.Duration) *AutoUpdater {
	return &AutoUpdater{
		sentinelURL: sentinelURL,
		binaryPath:  binaryPath,
		interval:    interval,
	}
}

// SetWatchdogHealthURL overrides the health endpoint the upgrade watchdog polls
// to confirm the new binary is live. Defaults to http://localhost:8080/health.
func (u *AutoUpdater) SetWatchdogHealthURL(url string) {
	u.watchdogHealthURL = url
}

// Run starts the auto-update loop. Blocks until ctx is cancelled.
func (u *AutoUpdater) Run(ctx context.Context) {
	log.Printf("[auto-update] started (check interval: %s, sentinel: %s)", u.interval, u.sentinelURL)

	// Wait before first check to let the daemon fully start
	select {
	case <-time.After(2 * time.Minute):
	case <-ctx.Done():
		return
	}

	ticker := time.NewTicker(u.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("[auto-update] stopped")
			return
		case <-ticker.C:
			if _, err := u.checkAndUpdate(ctx, false); err != nil {
				log.Printf("[auto-update] check failed: %v", err)
			}
		}
	}
}

// TriggerNow runs an upgrade check immediately instead of waiting for the next
// tick. Returns whether the binary was swapped. With force=true it re-pulls and
// swaps even when the sentinel-served binary already matches the running one
// (useful to force a restart onto the sentinel's binary). On success the daemon
// restarts, so callers should treat a nil error as "upgrade started" and
// confirm via the post-restart version. #354 Phase B.
func (u *AutoUpdater) TriggerNow(ctx context.Context, force bool) (bool, error) {
	return u.checkAndUpdate(ctx, force)
}

func (u *AutoUpdater) checkAndUpdate(ctx context.Context, force bool) (bool, error) {
	// 1. Get remote checksum
	remoteChecksum, err := u.getRemoteChecksum(ctx)
	if err != nil {
		return false, fmt.Errorf("get remote checksum: %w", err)
	}

	// 2. Get local checksum
	localChecksum, err := u.getLocalChecksum()
	if err != nil {
		return false, fmt.Errorf("get local checksum: %w", err)
	}

	// 3. Compare
	if remoteChecksum == localChecksum && !force {
		return false, nil // up to date
	}
	if remoteChecksum == localChecksum {
		log.Printf("[auto-update] forced upgrade; binary already matches sentinel (%s...)", localChecksum[:12])
	} else {
		log.Printf("[auto-update] new version detected (local=%s..., remote=%s...)", localChecksum[:12], remoteChecksum[:12])
	}

	// 4. Download new binary
	tmpPath := u.binaryPath + ".new"
	if err := u.downloadBinary(ctx, tmpPath); err != nil {
		_ = os.Remove(tmpPath)
		return false, fmt.Errorf("download: %w", err)
	}

	// 5. Verify downloaded binary checksum
	dlChecksum, err := checksumFile(tmpPath)
	if err != nil {
		_ = os.Remove(tmpPath)
		return false, fmt.Errorf("verify download: %w", err)
	}
	if dlChecksum != remoteChecksum {
		_ = os.Remove(tmpPath)
		return false, fmt.Errorf("checksum mismatch after download (got %s, want %s)", dlChecksum[:12], remoteChecksum[:12])
	}
