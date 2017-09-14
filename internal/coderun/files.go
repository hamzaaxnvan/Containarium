package coderun

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
)

// Box-side file access over the session agent-box already holds open (#1727).
//
// `code run` has to do two things before process_start: read the box's
// code.json to learn which engine it was installed with, and write a fresh
// 0600 gateway.env carrying that run's token. Both go through agent-box's
// own read_file / write_file tools on the EXISTING MCP session rather than a
// second ssh invocation.
//
// Two reasons it is write_file and not `shell_exec "printf ... > file"`:
//
//   - The token would otherwise be a shell command argument, which puts it in
//     the box's process table for the life of the call. docs/integrations/pi.md
//     opens by warning against exactly that. write_file carries the content as
//     an MCP argument over the already-encrypted stdio channel instead.
//   - write_file chmods the temp file BEFORE renaming it into place
//     (internal/agentbox/files.go), so the file never exists at its final path
//     with a wider mode. That is the same "umask before the write, not chmod
//     after" property the boxbootstrap apply.sh argues for, and it is why a
//     0600 request here is actually 0600 for the file's whole life.

// GatewayEnvFileMode is the mode a box's gateway.env is written with. A bearer
// token that spends someone's inference budget gets owner-only, always.
const GatewayEnvFileMode = "0600"

// CodeConfigFileMode is the mode code.json is written with (contract C4).
const CodeConfigFileMode = "0600"

// HomeDir returns the box user's home directory.
//
// It exists because agent-box's file tools take real paths — they do not expand
// `~` or `$HOME`, and filepath.Abs resolves a relative path against agent-box's
// own cwd, which is an implementation detail this package should not bet a
// credential's location on. One cheap shell_exec makes every subsequent path
// absolute and explicit. The command carries no secret.
func (s *Session) HomeDir(ctx context.Context) (string, error) {
	text, err := s.callTool(ctx, "shell_exec", map[string]any{
		"command": `printf '%s' "$HOME"`,
	}, true)
	if err != nil {
		return "", fmt.Errorf("resolve $HOME on the box: %w", err)
	}
	// shell_exec frames its reply as key: value plus a stdout section; the
	// home directory is the only non-empty line of that stdout.
	home := ""
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasSuffix(line, ":") || strings.Contains(line, ": ") {
			continue
		}
		if strings.HasPrefix(line, "/") {
			home = line
			break
		}
	}
	if home == "" {
		return "", fmt.Errorf("could not read $HOME from the box (shell_exec said: %s)", strings.TrimSpace(text))
	}
	return home, nil
}

// ExpandHome rewrites a "$HOME/..."-rooted path, of the kind the engine package
// embeds in shell scripts, into an absolute path for agent-box's file tools.
//
// The engine package deliberately writes its paths as `$HOME/...` because they
// are mostly interpolated into shell scripts, where the shell expands them.
// The file tools are the one consumer that needs them pre-expanded, so the
// translation lives here rather than duplicating every path as two constants.
func ExpandHome(home, p string) string {
	switch {
	case strings.HasPrefix(p, "$HOME/"):
		return path.Join(home, strings.TrimPrefix(p, "$HOME/"))
	case p == "$HOME":
		return home
	case strings.HasPrefix(p, "~/"):
		return path.Join(home, strings.TrimPrefix(p, "~/"))
	default:
		return p
	}
}

// ErrFileNotFound reports that a box-side file does not exist — as distinct from
// "exists but could not be read".
//
// The distinction is load-bearing for `code run`, which falls back to the
// pre-#1727 Claude + tenant-secret default when a box has no code.json. Treating
// every read failure as absence would make a transport blip or a permission
// error on a pi/gateway box silently run the wrong engine on the wrong
// credential, and swallow the real failure while doing it. So callers that mean
// "absent" must ask for exactly this, and nothing else may satisfy them.
var ErrFileNotFound = errors.New("file not found on the box")

// ReadFile reads absPath from the box.
//
// A confirmed missing file is reported as ErrFileNotFound; every other failure —
// permissions, a sandbox-root refusal, a directory, a dead transport — is
// returned as an ordinary error carrying agent-box's own reason.
func (s *Session) ReadFile(ctx context.Context, absPath string) (string, error) {
	text, err := s.callTool(ctx, "read_file", map[string]any{"path": absPath}, true)
	if err != nil {
		if isNotExistMessage(err.Error()) {
			return "", fmt.Errorf("read_file %s: %w", absPath, ErrFileNotFound)
		}
