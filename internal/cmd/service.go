//go:build !windows && !containarium_client

package cmd

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/footprintai/containarium/internal/hostcheck"
)

const systemdServicePath = "/etc/systemd/system/containarium.service"

// incusCPUWeightDropInPath is where the platform CPU weight for incusd lands
// (#2284). incus.service belongs to the distro / Zabbly package, so it is
// never edited in place — a drop-in is the only edit that survives a package
// upgrade. Numbered 50- so an operator's own drop-in (sorted later) wins.
const incusCPUWeightDropInPath = "/etc/systemd/system/incus.service.d/50-containarium-cpu-weight.conf"

// compatSymlinkOldPath / compatSymlinkNewPath name the Phase 1 rollout
// compat symlink (design doc, Rollout Phase 1, #1780): for the duration of
// the containariumd rollout, /usr/local/bin/containarium keeps working as a
// symlink to /usr/local/bin/containariumd, so any runbook, cron, or script
// this inventory missed still works — and shows up in the audit log as the
// old name rather than failing outright.
const (
	compatSymlinkOldPath = "/usr/local/bin/containarium"
	compatSymlinkNewPath = "/usr/local/bin/containariumd"
)

// ensureCompatSymlink makes compatSymlinkOldPath a symlink to
// compatSymlinkNewPath, idempotently. root is prepended to both paths so
// tests can exercise this without touching the real filesystem; callers
// pass "/".
//
// Deliberately a no-op when compatSymlinkNewPath doesn't exist yet:
// `service install` / `pool join` can run before this host's normal deploy
// path (terraform startup script reconcile, self-update) has actually
// placed containariumd, and replacing a still-in-use containarium binary
// with a symlink to nothing would brick the host instead of converging it.
// Both callers are documented idempotent, so the next re-run picks up the
// symlink once the binary exists.
func ensureCompatSymlink(root string) error {
	newPath := filepath.Join(root, compatSymlinkNewPath)
	if _, err := os.Stat(newPath); err != nil {
		return nil
	}
	oldPath := filepath.Join(root, compatSymlinkOldPath)
	if target, err := os.Readlink(oldPath); err == nil && target == newPath {
		return nil // already correct
	}
	if err := os.Remove(oldPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove %s before symlinking to %s: %w", oldPath, newPath, err)
	}
	if err := os.Symlink(newPath, oldPath); err != nil {
		return fmt.Errorf("failed to symlink %s -> %s: %w", oldPath, newPath, err)
	}
	log.Printf("Compat symlink: %s -> %s", oldPath, newPath)
	return nil
}

// removeCompatSymlink removes compatSymlinkOldPath only if it is currently
// our symlink to compatSymlinkNewPath — never a real file. Uninstall must
// not delete a binary some other mechanism placed there.
func removeCompatSymlink(root string) {
	oldPath := filepath.Join(root, compatSymlinkOldPath)
	newPath := filepath.Join(root, compatSymlinkNewPath)
	if target, err := os.Readlink(oldPath); err == nil && target == newPath {
		_ = os.Remove(oldPath)
	}
}

// systemdServiceTemplate is the canonical systemd service file.
// The daemon self-bootstraps from PostgreSQL so only --rest and --jwt-secret-file are needed.
const systemdServiceTemplate = `[Unit]
Description=Containarium Container Management Daemon
Documentation=https://github.com/footprintai/Containarium
After=network.target incus.service
# Wants=, not Requires=. A Requires= dependency that fails takes this unit's
# start job down with it, and a *job* failure is not something Restart=on-failure
# retries -- so a single transient incus hiccup at boot leaves the daemon dead
# until someone notices, which on a pool member means silently dropping out of
# the pool. After= still guarantees ordering, and Restart=on-failure covers the
# "incus not ready yet" case by retrying the daemon itself.
Wants=incus.service
StartLimitIntervalSec=0

[Service]
Type=simple
ExecStart=/usr/local/bin/containariumd daemon \
  --rest \
  --jwt-secret-file /etc/containarium/jwt.secret
Restart=on-failure
# Keep retrying forever (StartLimitIntervalSec=0 above), but not at full
# speed. A persistent start failure used to retry every 5s indefinitely:
# one production host crash-looped 1655 times in ~4 hours, pinning a core
# and — because each cycle rewrites the bundled monitoring config —
# restarting alertmanager and vmalert every 7 seconds, so alerting could
# not have fired during the incident, including on the incident itself
# (#1152).
#
# "Never give up" and "retry at full speed" are separable. RestartSteps
# interpolates the delay geometrically from RestartSec to
# RestartMaxDelaySec, so a transient incus hiccup at boot still recovers
# in seconds while a persistent failure settles to one attempt every 5
# minutes. That same 4-hour outage would cost ~50 attempts, not 1655.
#
# Requires systemd 254+. The documented host floor is Ubuntu 24.04 LTS
# ("or later", README), which ships 255. On an older host systemd warns
# and continues, degrading to the previous fixed-interval behaviour
# rather than failing to start — so this cannot brick a host that
# predates the directives.
RestartSec=5s
RestartSteps=6
RestartMaxDelaySec=5min
User=root
Group=root

# Memory accounting only — deliberately no MemoryMax here (#1350).
#
# The sentinel unit caps its memory, because it is a pure forwarder whose
# working set is small and well understood (~82 MB) and whose host is 1-2 GB.
# The daemon is not that: it manages containers, ZFS datasets and gRPC traffic,
# and its legitimate peak is workload-dependent. A guessed cap on the busiest
# component risks restart loops under normal load, which is a worse failure
# than the slow leak a cap guards against.
#
# Accounting is the prerequisite either way: it costs nothing, exposes the
