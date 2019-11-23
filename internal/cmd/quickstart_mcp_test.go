package cmd

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

const wantAgentBoxRemoteCommand = `sh -c 'PATH="$HOME/.local/bin:/usr/local/bin:$PATH" exec agent-box'`

func codexMCPServerFromFile(t *testing.T, path, name string) mcpServerEntry {
	t.Helper()
	var config struct {
		Servers map[string]mcpServerEntry `toml:"mcp_servers"`
	}
	if _, err := toml.Decode(readFile(t, path), &config); err != nil {
		t.Fatalf("parse TOML: %v", err)
	}
	entry, ok := config.Servers[name]
	if !ok {
		t.Fatalf("missing MCP server %q", name)
	}
	return entry
}

func TestQuickstartMCP_NonInteractiveShell(t *testing.T) {
	for _, agent := range supportedAgents {
		t.Run(agent, func(t *testing.T) {
			// A space in HOME also proves the remote shell preserves quoting.
			home := filepath.Join(t.TempDir(), "box home")
			bin := filepath.Join(home, ".local", "bin")
			if err := os.MkdirAll(bin, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(bin, "agent-box"), []byte("#!/bin/sh\nprintf '%s\\n' \"$PATH\"\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			spec := agentSpecs[agent]
			path := spec.mcpConfigPath(t.TempDir())
			if _, err := spec.wireMCP(path, "containarium-box", "alice"); err != nil {
				t.Fatal(err)
			}
			var entry mcpServerEntry
			if agent == "codex" {
				entry = codexMCPServerFromFile(t, path, "containarium-box")
			} else {
				entry = mcpServerFromFile(t, path, "mcpServers", "containarium-box")
			}
			if entry.Command != "ssh" || len(entry.Args) != 2 || entry.Args[0] != "alice" {
				t.Fatalf("MCP entry = %+v", entry)
			}
			// ssh passes its remote command to a non-interactive shell. Execute
			// that same command with no login files or user-local PATH entry.
			cmd := exec.Command("/bin/sh", "-c", entry.Args[1])
			cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin"}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("remote command failed: %v\n%s", err, out)
			}
			if want := bin + ":/usr/local/bin:/usr/bin:/bin\n"; string(out) != want {
				t.Fatalf("helper PATH = %q, want %q", out, want)
			}
			if entry.Args[1] != wantAgentBoxRemoteCommand {
				t.Fatalf("remote command = %q, want %q", entry.Args[1], wantAgentBoxRemoteCommand)
			}
		})
	}
}

func TestMergeMCPServerJSON_UpgradeLegacy(t *testing.T) {
	for _, tc := range []struct {
		name, entry string
		upgrade     bool
	}{
		{"generated", `{"command":"ssh","args":["alice","agent-box"]}`, true},
		{"extra settings", `{"command":"ssh","args":["alice","agent-box"],"env":{"FOO":"bar"},"timeout":30}`, true},
		{"different host", `{"command":"ssh","args":["bob","agent-box"]}`, false},
		{"custom command", `{"command":"custom","args":["alice","agent-box"]}`, false},
		{"custom flags", `{"command":"ssh","args":["-T","alice","agent-box"]}`, false},
		{"explicit path", `{"command":"ssh","args":["alice","~/.local/bin/agent-box"]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			seed := `{"theme":"dark","mcpServers":{"other":{"command":"foo"},"containarium-box":` + tc.entry + `}}`
			if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
				t.Fatal(err)
			}
