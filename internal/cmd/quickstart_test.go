package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// noOwn is the uid/gid pair meaning "don't chown" (not running under sudo).
const noOwn = -1

func TestEnsureSSHInclude(t *testing.T) {
	include := "/home/alice/.containarium/ssh_config"

	t.Run("creates file and line when missing", func(t *testing.T) {
		dir := t.TempDir()
		cfg := filepath.Join(dir, ".ssh", "config")

		added, err := ensureSSHInclude(cfg, include, noOwn, noOwn)
		if err != nil {
			t.Fatalf("ensureSSHInclude: %v", err)
		}
		if !added {
			t.Fatal("expected added=true on first run")
		}
		got := readFile(t, cfg)
		if !strings.Contains(got, "Include "+include) {
			t.Fatalf("Include line missing:\n%s", got)
		}
	})

	t.Run("idempotent — second run is a no-op", func(t *testing.T) {
		dir := t.TempDir()
		cfg := filepath.Join(dir, ".ssh", "config")

		if _, err := ensureSSHInclude(cfg, include, noOwn, noOwn); err != nil {
			t.Fatalf("first run: %v", err)
		}
		before := readFile(t, cfg)

		added, err := ensureSSHInclude(cfg, include, noOwn, noOwn)
		if err != nil {
			t.Fatalf("second run: %v", err)
		}
		if added {
			t.Fatal("expected added=false on second run")
		}
		if after := readFile(t, cfg); after != before {
			t.Fatalf("second run mutated the file:\nbefore=%q\nafter=%q", before, after)
		}
		if n := strings.Count(readFile(t, cfg), "Include "+include); n != 1 {
			t.Fatalf("expected exactly 1 Include line, got %d", n)
		}
	})

	t.Run("preserves existing content and prepends", func(t *testing.T) {
		dir := t.TempDir()
		cfg := filepath.Join(dir, "config")
		existing := "Host bastion\n  HostName 10.0.0.1\n"
		if err := os.WriteFile(cfg, []byte(existing), 0o600); err != nil {
			t.Fatal(err)
		}

		added, err := ensureSSHInclude(cfg, include, noOwn, noOwn)
		if err != nil {
			t.Fatalf("ensureSSHInclude: %v", err)
		}
		if !added {
			t.Fatal("expected added=true")
		}
		got := readFile(t, cfg)
		if !strings.HasPrefix(got, "Include "+include+"\n") {
			t.Fatalf("Include should be prepended, got:\n%s", got)
		}
		if !strings.Contains(got, existing) {
			t.Fatalf("existing content lost:\n%s", got)
		}
	})
}

func TestMergeMCPServerJSON(t *testing.T) {
	const key = "mcpServers"

	t.Run("creates config with the box server", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, ".claude.json")

		changed, err := mergeMCPServerJSON(path, key, "containarium-box", "alice")
		if err != nil {
			t.Fatalf("merge: %v", err)
		}
		if !changed {
			t.Fatal("expected changed=true")
		}
		entry := mcpServerFromFile(t, path, key, "containarium-box")
		if entry.Command != "ssh" {
			t.Fatalf("command = %q, want ssh", entry.Command)
		}
		if len(entry.Args) != 2 || entry.Args[0] != "alice" || entry.Args[1] != wantAgentBoxRemoteCommand {
			t.Fatalf("args = %v, want alice and the PATH-prefixed remote command", entry.Args)
		}
	})

	t.Run("preserves unrelated keys", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, ".claude.json")
		seed := `{"theme":"dark","mcpServers":{"other":{"command":"foo","args":["bar"]}}}`
		if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
			t.Fatal(err)
		}

		if _, err := mergeMCPServerJSON(path, key, "containarium-box", "alice"); err != nil {
			t.Fatalf("merge: %v", err)
		}

		var root map[string]any
		if err := json.Unmarshal([]byte(readFile(t, path)), &root); err != nil {
			t.Fatalf("result not valid JSON: %v", err)
		}
		if root["theme"] != "dark" {
			t.Fatalf("top-level key lost: theme=%v", root["theme"])
		}
		servers := root[key].(map[string]any)
		if _, ok := servers["other"]; !ok {
			t.Fatal("pre-existing 'other' server was dropped")
		}
		if _, ok := servers["containarium-box"]; !ok {
			t.Fatal("containarium-box not added")
		}
	})

	t.Run("does not clobber an existing entry", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, ".claude.json")
		seed := `{"mcpServers":{"containarium-box":{"command":"custom","args":["x"]}}}`
		if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
			t.Fatal(err)
		}

		changed, err := mergeMCPServerJSON(path, key, "containarium-box", "alice")
		if err != nil {
			t.Fatalf("merge: %v", err)
		}
		if changed {
			t.Fatal("expected changed=false — must not clobber user's entry")
		}
		if entry := mcpServerFromFile(t, path, key, "containarium-box"); entry.Command != "custom" {
			t.Fatalf("user entry was overwritten: command=%q", entry.Command)
		}
	})

	t.Run("errors on invalid JSON", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, ".claude.json")
		if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := mergeMCPServerJSON(path, key, "containarium-box", "alice"); err == nil {
			t.Fatal("expected an error on invalid JSON, got nil")
		}
	})
}

func TestCodexAppendMCP(t *testing.T) {
	t.Run("appends a table when missing", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, ".codex", "config.toml")

		changed, err := codexAppendMCP(path, "containarium-box", "alice")
		if err != nil {
			t.Fatalf("append: %v", err)
		}
		if !changed {
			t.Fatal("expected changed=true")
		}
		got := readFile(t, path)
		for _, want := range []string{
			"[mcp_servers.containarium-box]",
			`command = "ssh"`,
			"args = [\"alice\", " + strconv.Quote(wantAgentBoxRemoteCommand) + "]",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("missing %q in:\n%s", want, got)
			}
		}
	})

	t.Run("preserves existing config and idempotent", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.toml")
		existing := "model = \"o3\"\n\n[mcp_servers.other]\ncommand = \"foo\"\n"
		if err := os.WriteFile(path, []byte(existing), 0o600); err != nil {
			t.Fatal(err)
		}

