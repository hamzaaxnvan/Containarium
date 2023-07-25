package container

import (
	"fmt"
	"strings"
)

// DefaultWorkspacePath is where git source lands in the box when the
// caller doesn't specify one. Matches the convention the CI flow
// (containarium-run) and the agent-box already assume. Exported so
// RunAgentSkill's git-source path (#1859) can report the same default in its
// response without duplicating the string.
const DefaultWorkspacePath = "/workspace"

// gitWorkspacePath returns the workspace path, defaulting to
// /workspace when the caller left it empty.
func gitWorkspacePath(p string) string {
	if strings.TrimSpace(p) == "" {
		return DefaultWorkspacePath
	}
	return p
}

// shellSingleQuote wraps s in single quotes for safe interpolation
// into a /bin/sh -c script, escaping any embedded single quotes.
// Used so a caller-supplied repo URL / ref / workspace path can't
// break out of string context in the fetch script.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// buildGitFetchScript returns the /bin/sh script the daemon runs
// inside the box to populate the workspace from a git remote.
//
// Design notes:
//   - The box base image may lack git; the script installs it via
//     whatever package manager is present before fetching.
//   - A shallow fetch of the exact ref keeps it fast + reproducible.
//     Empty ref fetches the remote's default branch (FETCH_HEAD).
//   - A private-repo credential is injected as an ephemeral
//     `http.extraHeader` on the single fetch invocation — it is
//     never written to the repo's .git/config, so it doesn't persist
//     in the box after provisioning. (Matches how actions/checkout
//     scopes its token, minus the on-disk config write.)
//   - All caller-supplied values are single-quoted to prevent shell
//     injection.
func buildGitFetchScript(repoURL, ref, credential, workspacePath string) string {
	ws := gitWorkspacePath(workspacePath)

	var fetch strings.Builder
	fetch.WriteString("git ")
	if credential != "" {
		// The header value carries the token; quote it as one arg.
		hdr := "AUTHORIZATION: bearer " + credential
		fetch.WriteString("-c http.extraHeader=" + shellSingleQuote(hdr) + " ")
	}
	fetch.WriteString("fetch --depth 1 -q " + shellSingleQuote(repoURL))
	if ref != "" {
		fetch.WriteString(" " + shellSingleQuote(ref))
	}

	// `set -e` so any step failing aborts with a non-zero exit the
	// daemon surfaces. git install is best-effort across apt/dnf/yum.
	return strings.Join([]string{
		"set -e",
		`if ! command -v git >/dev/null 2>&1; then`,
		`  (apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq git) \`,
		`    || (dnf install -y -q git) || (yum install -y -q git) \`,
		`    || { echo "git unavailable and auto-install failed" >&2; exit 1; }`,
		`fi`,
