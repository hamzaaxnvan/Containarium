// Package transfer ships files from the local workstation into a remote
// Containarium container, via the same SSH path the demo flow uses
// (laptop → sentinel → sshpiper → backend → containarium-shell → incus exec).
//
// Two entry-points serving two mental models:
//
//   - Push: ships committed git history via `git bundle`. Atomic per
//     commit. Refuses dirty working trees unless IncludeWIP is set.
//
//   - Sync: mirrors the working directory (including .git/) via a manual
//     content-hash diff + tar of changed files. Pushes uncommitted +
//     untracked + stash refs alongside committed history. Delta-only on
//     subsequent calls.
//
// Both use one-shot ssh-with-command invocations rather than bidirectional
// protocols (git-receive-pack, rsync --server) — those are fragile through
// our shell stack.
package transfer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/footprintai/containarium/internal/config"
)

// ErrSentinelUnresolved is returned by resolve when no SSH endpoint could
// be determined — neither an explicit SentinelHost, the container's
// daemon-stamped ssh_host (tried by callers before reaching here), nor
// $CONTAINARIUM_SENTINEL_HOST. Callers (notably the MCP push/sync
// handlers) check this with errors.Is to attach surface-specific guidance
// instead of leaving the agent to chase the env var.
var ErrSentinelUnresolved = errors.New("sentinel host not set")

// Options carries the inputs both Push and Sync need.
type Options struct {
	// Username — the container's user. Maps to the ssh user the
	// sentinel routes through sshpiper.
	Username string

	// SentinelHost — the public SSH endpoint, e.g. "34.42.156.100" or
	// "sentinel.example.com". When empty, transfer looks up
	// $CONTAINARIUM_SENTINEL_HOST.
	SentinelHost string

	// KeyPath — path to the SSH private key for Username. When empty,
	// defaults to ~/.containarium/keys/<Username>.
	KeyPath string

	// LocalPath — local file or directory being shipped. For Push,
	// must be a git repo (or contain one at LocalPath/.git). Defaults
	// to the caller's cwd when empty.
	LocalPath string

	// RemotePath — destination inside the container. Defaults to
	// "/home/<Username>/work" when empty. The directory is created on
