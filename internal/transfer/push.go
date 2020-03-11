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
		out, err := runGit(opt.LocalPath, "rev-parse", "--abbrev-ref", "HEAD")
		if err != nil {
			return nil, fmt.Errorf("detect current branch: %w", err)
		}
		branch = strings.TrimSpace(out)
		if branch == "" || branch == "HEAD" {
			return nil, fmt.Errorf("detached HEAD; pass --branch")
		}
	}

	// Working-tree-dirty handling.
	dirty, err := isWorkingTreeDirty(opt.LocalPath)
	if err != nil {
		return nil, fmt.Errorf("check working tree: %w", err)
	}
	wipCommitMade := false
	var wipSha string
	if dirty && !opt.IncludeWIP {
		return nil, fmt.Errorf("working tree has uncommitted changes; commit first, or pass --include-wip to auto-create a WIP commit")
	}
	if dirty && opt.IncludeWIP {
		sha, err := makeWIPCommit(opt.LocalPath)
		if err != nil {
			return nil, fmt.Errorf("make WIP commit: %w", err)
		}
		wipSha = sha
		wipCommitMade = true
		defer func() {
			// Rewind the WIP commit so the local repo stays clean. Files
			// in the index/working tree are restored via `git reset
			// --mixed`.
			_, _ = runGit(opt.LocalPath, "reset", "--mixed", wipSha+"^")
		}()
	}

	// Provision (or refresh) the bare repo + post-receive hook on the
	// container. Idempotent: re-running with the same DeployCmd is a
	// no-op aside from rewriting the hook (cheap).
	if err := ensureRemoteSetup(opt, branch); err != nil {
		return nil, fmt.Errorf("remote setup: %w", err)
	}

	// Configure local git remote so subsequent ad-hoc `git push
	// containarium-<user>` from the same clone works without involving
	// the CLI.
	remoteName := opt.RemoteName
	if remoteName == "" {
		remoteName = "containarium-" + opt.Username
	}
	remoteURL := remoteRepoSSHURL(opt)
	if err := ensureLocalRemote(opt.LocalPath, remoteName, remoteURL); err != nil {
		return nil, fmt.Errorf("configure local remote: %w", err)
	}

	// Capture the remote branch's current sha (if any) BEFORE the push,
	// so we can report previousHead → newHead.
	previousHead, _ := remoteHead(opt, branch)

	// Actually push. Use GIT_SSH_COMMAND so git inherits our key +
	// IdentitiesOnly + no-strict-host-key options. Without
	// IdentitiesOnly, every key in ~/.ssh/ gets offered to sshpiper's
	// failtoban — see PR #132 for the original symptom.
	if err := gitPush(opt, remoteName, branch); err != nil {
		return nil, fmt.Errorf("git push: %w", err)
	}

	// Capture new head.
	newHead, err := runGit(opt.LocalPath, "rev-parse", branch)
	if err != nil {
		return nil, fmt.Errorf("resolve new head: %w", err)
	}

	return &PushResult{
		Branch:        branch,
		NewHead:       strings.TrimSpace(newHead),
		PreviousHead:  previousHead,
		RemoteURL:     remoteURL,
		WIPCommitMade: wipCommitMade,
		SetupRan:      true,
		DeployCmd:     opt.DeployCmd,
	}, nil
}

// remoteRepoSSHURL builds the ssh-style remote URL git push consumes.
// Convention: bare repo lives at "<RemotePath>.git" inside the
// container's home dir; the work tree is at "<RemotePath>" (the hook
// checks out into it).
func remoteRepoSSHURL(opt PushOptions) string {
	// git accepts <user>@<host>:<path-relative-to-home>. So we strip a
	// leading $HOME/ if the caller wrote an absolute path.
	bare := strings.TrimPrefix(opt.RemotePath, "/home/"+opt.Username+"/")
	bare = strings.TrimPrefix(bare, "~/")
	if strings.HasPrefix(bare, "/") {
		// Absolute path that's not under the user's home — pass through
		// as-is.
		return fmt.Sprintf("%s@%s:%s.git", opt.Username, opt.SentinelHost, bare)
	}
	return fmt.Sprintf("%s@%s:%s.git", opt.Username, opt.SentinelHost, bare)
}

// hookData feeds the post-receive template.
type hookData struct {
	Branch    string
	BareRepo  string
	WorkTree  string
	DeployCmd string
}

const hookTemplate = `#!/bin/sh
set -e
while read oldrev newrev refname; do
    branch="${refname#refs/heads/}"
    if [ "$branch" = "{{.Branch}}" ]; then
        echo "[containarium] post-receive: $branch $oldrev..$newrev" >&2
        GIT_WORK_TREE={{.WorkTree}} git --git-dir={{.BareRepo}} checkout -f "$branch"
{{- if .DeployCmd}}
        cd {{.WorkTree}}
        {{.DeployCmd}}
{{- end}}
    fi
done
`

// renderHook produces the post-receive hook body for the given options.
// Split out for testability.
func renderHook(d hookData) (string, error) {
	t, err := template.New("hook").Parse(hookTemplate)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, d); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// ensureRemoteSetup creates the bare repo + post-receive hook + work
// tree directory on the container if missing, and refreshes the hook
// to reflect the current DeployCmd. Idempotent.
//
// One SSH connection per push for setup. The actual `git push` opens
// its own SSH connection; we could share if we cared about latency,
// but ~200ms once per push is fine for v1.
func ensureRemoteSetup(opt PushOptions, branch string) error {
	bareRepo := opt.RemotePath + ".git"
	workTree := opt.RemotePath
	hook, err := renderHook(hookData{
		Branch:    branch,
		BareRepo:  bareRepo,
