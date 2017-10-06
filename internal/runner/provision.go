package runner

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// MaxRunnerCount is the upper bound on how many runners a single
// provision call will create. 100 is generous for most teams (Cloud
// Run on most plans caps concurrent jobs far below this) and small
// enough that an agent typo (`count: 1000`) doesn't accidentally
// spin up a fleet that bleeds the cloud bill before the operator
// notices.
const MaxRunnerCount = 100

// DefaultBoxCreateTimeout caps how long we'll wait for a single
// box to come up and accept SSH. 5 min covers the Incus create →
// cloud-init → sshd-ready path with comfortable headroom for slow
// images; failing fast above that lets the caller move on and a
// human investigate rather than the agent blocking forever.
const DefaultBoxCreateTimeout = 5 * time.Minute

// DefaultInstallTimeout caps the runner install step. The script
// apt-installs a handful of packages plus toolchains (Go, Node,
// buf, golangci-lint) — on a cold box that can take 3-4 minutes
// over a slow link. 10 min is "generous-but-bounded".
const DefaultInstallTimeout = 10 * time.Minute

// DefaultSSHReadyTimeout bounds how long provision waits for a freshly-
// created box to become reachable via the sentinel before the first
// install-state probe. A new box isn't SSH-able the instant it reaches
// RUNNING: its sshd has to come up AND the sentinel keysync has to
// propagate the box's host-side authorized_keys into sshpiper's per-box
// gate — a periodic poll that can take a couple of minutes. Until then
// the probe is rejected publickey / dial-refused, which is transient, not
// a real failure. Comfortably over the observed keysync interval. (#475)
const DefaultSSHReadyTimeout = 5 * time.Minute

// sshProbeInitialBackoff is the first inter-probe wait in
// waitForSSHInstalled; it doubles up to sshProbeMaxBackoff. A package var
// so tests can shrink it to keep the retry loop instant.
var sshProbeInitialBackoff = 3 * time.Second

const sshProbeMaxBackoff = 15 * time.Second

// waitForSSHInstalled retries the install-state probe until the box is
// reachable via the sentinel (sshd up + keysync done) or readyTimeout /
// ctx expires. A freshly-created box's first probes fail publickey/dial;
// those are transient here, so we back off and retry rather than failing
// the whole provision on the first miss. Returns IsInstalled's result once
// SSH succeeds. (#475)
func waitForSSHInstalled(ctx context.Context, ssh RunnerInstaller, name string, readyTimeout time.Duration) (bool, error) {
	deadline := time.Now().Add(readyTimeout)
	backoff := sshProbeInitialBackoff
	var lastErr error
	for {
		installed, err := ssh.IsInstalled(ctx, name)
		if err == nil {
			return installed, nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			return false, fmt.Errorf("box not reachable via sentinel within %s (sshd/keysync not ready): %w", readyTimeout, lastErr)
		}
		select {
		case <-ctx.Done():
			return false, fmt.Errorf("box not reachable via sentinel: %w", lastErr)
		case <-time.After(backoff):
		}
		if backoff < sshProbeMaxBackoff {
			if backoff *= 2; backoff > sshProbeMaxBackoff {
				backoff = sshProbeMaxBackoff
			}
		}
	}
}

// DefaultRegistrationTimeout caps the post-install poll waiting
// for the runner to appear in GitHub's runners list. systemd
// usually starts the service within a couple of seconds; we give
// 60 s so a slow `actions-runner` tarball extract on the box
// doesn't trip the wait. Use exponential backoff so we don't
// hammer the GitHub API.
const DefaultRegistrationTimeout = 60 * time.Second

// Options is the typed input to Provision. Field defaults are
// applied by Provision when the field is the zero value, so a
// caller that wants the defaults can pass `Options{Repo: "...",
// PAT: "...", Count: 1}` and not worry about timeouts.
type Options struct {
	// Repo is the registration target: "owner/repo" for a single
	// repository, or a bare "owner" for an organization (#1217). The shape
	// selects the endpoint family, the way GitHub itself disambiguates.
	// Named Repo for compatibility with existing callers.
	Repo string

	// PAT is a GitHub Personal Access Token with the `repo` scope for a
	// repository target, or `admin:org` for an organization target.
	// Used both to mint registration tokens (inside the box, via
	// the install script) and to query the runners-list API to
	// confirm registration succeeded.
	PAT string

	// Count is the number of runners to provision. 1..MaxRunnerCount.
	Count int

	// NamePrefix is the prefix used when generating box names via
	// NameTemplate. Default: "ci-runner".
	NamePrefix string

	// Labels is the comma-separated label list applied to each
	// runner. Default: "containarium,ephemeral". Workflows target
	// with `runs-on: [self-hosted, <labels>...]`.
	Labels string

	// RunnerGroup is the organization runner group each runner joins
	// (config.sh --runnergroup). Organization targets only: GitHub has no
	// repository-level groups. Empty leaves the runner in the org's
	// default group, and passes nothing extra to the install script.
	RunnerGroup string

	// NameTemplate is the Go-style template used to build each
	// runner's box name. Two placeholders are supported and
	// substituted by simple string replace:
	//   {prefix}  → NamePrefix
	//   {i}       → 1-based runner index
	// Default: "{prefix}-{i}".
	NameTemplate string

	// SSHKey is an optional override of the SSH public key used
	// when creating boxes. Empty means the BoxManager picks a
	// default (most implementations generate an ephemeral key).
	SSHKey string

	// BoxCreateTimeout / InstallTimeout / RegistrationTimeout are
	// per-step deadlines. Zero falls back to the Default* values.
	BoxCreateTimeout    time.Duration
	InstallTimeout      time.Duration
	RegistrationTimeout time.Duration

	// MaxTotal is the system-wide ceiling on how many runner boxes
	// (names matching NamePrefix) may exist at once, across every
	// caller. Provision counts the live fleet and creates at most
