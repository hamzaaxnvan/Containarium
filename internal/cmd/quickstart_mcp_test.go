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
