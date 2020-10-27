package transfer

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
)

// PushOptions extends Options with push-specific knobs.
type PushOptions struct {
	Options

	// Branch — git branch to push. Empty → uses current HEAD branch.
	Branch string

	// IncludeWIP — when set, uncommitted changes are auto-wrapped in a
	// WIP commit before pushing, then rewound after. Off by default,
	// matching the "git ideology" contract: commits ship, working tree
	// changes don't.
	IncludeWIP bool

	// DeployCmd — when set, the container-side post-receive hook
	// embeds this shell command and runs it (inside the container's
	// work tree directory) after each successful push. Use it to
	// restart the service, run migrations, rebuild artifacts, etc.
	// Empty deploys the source but runs no command — operator can
	// run anything manually via ssh.
	DeployCmd string

	// RemoteName — the local git remote name to (re)configure. Default
	// "containarium-<username>" so multiple containers can be pushed
	// to from the same local clone without colliding.
	RemoteName string
}

// PushResult summarizes the push.
type PushResult struct {
	Branch        string
	NewHead       string // sha now at the remote tip
	PreviousHead  string // sha that was at remote tip before this push; "" on first push
	RemoteURL     string // ssh-style URL we pushed to, for the caller's records
	WIPCommitMade bool
	SetupRan      bool   // true if we provisioned/refreshed the bare repo + hook this call
	DeployCmd     string // echoed back so callers can audit
}

// Push ships committed git history to the container by running
// `git push` against a container-hosted bare repo. The container side
// installs a post-receive hook on first call (and rewrites it any time
// DeployCmd changes) that checks out the working tree and optionally
// runs DeployCmd.
//
// Why real git push and not a bundle: receive-pack works through our
// SSH stack (sshpiper -> sshd -> containarium-shell -> su -c -> incus
// exec) — empirically verified before this PR. Real git push gives the
// agent + any vanilla git client the same single mechanism, and it
// includes a post-receive hook execution surface for free.
func Push(opt PushOptions) (*PushResult, error) {
	if err := opt.resolve(); err != nil {
		return nil, err
	}

	gitDir := filepath.Join(opt.LocalPath, ".git")
	if _, err := os.Stat(gitDir); err != nil {
		return nil, fmt.Errorf("local path %s is not a git repository (no .git directory): %w", opt.LocalPath, err)
	}

	// Resolve branch.
	branch := opt.Branch
	if branch == "" {
