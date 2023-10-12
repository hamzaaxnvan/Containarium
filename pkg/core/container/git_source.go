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
