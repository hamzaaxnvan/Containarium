package sshconfig

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// The regression this file exists for: a sync whose enumeration came back
// empty overwrote a 44-host config with an empty file and reported success.
func TestWriteConfig_RefusesToWipeNonEmptyConfigWithZeroHosts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ssh_config")
	existing := "Host box-a\n  HostName 10.0.0.1\n"
	if err := os.WriteFile(path, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := WriteConfig(path, Generated{Content: "", Count: 0}, false)
	if !errors.Is(err, ErrRefusedEmptyOverwrite) {
		t.Fatalf("want ErrRefusedEmptyOverwrite, got %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != existing {
		t.Fatalf("existing config was modified:\n%q", got)
	}
}

func TestWriteConfig_ForceAllowsTheWipe(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ssh_config")
	if err := os.WriteFile(path, []byte("Host box-a\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	backedUp, err := WriteConfig(path, Generated{Content: "", Count: 0}, true)
	if err != nil {
		t.Fatalf("force write: %v", err)
	}
	if !backedUp {
		t.Error("force overwrite should still leave a .bak")
	}
	got, _ := os.ReadFile(path)
	if len(got) != 0 {
		t.Fatalf("want empty after forced write, got %q", got)
	}
	bak, err := os.ReadFile(path + ".bak")
	if err != nil || string(bak) != "Host box-a\n" {
		t.Fatalf("backup missing or wrong: %q (%v)", bak, err)
	}
}

func TestWriteConfig_TableDriven(t *testing.T) {
	tests := []struct {
		name        string
		existing    string // "" = no file
		gen         Generated
		force       bool
		wantErr     bool
		wantContent string
		wantBackup  bool
