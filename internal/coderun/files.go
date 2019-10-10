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
