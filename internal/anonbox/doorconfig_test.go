package anonbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoorStore_MissingFileIsOpen(t *testing.T) {
	s := NewDoorStore(filepath.Join(t.TempDir(), "anon-door.json"))
	if s.Err() != nil {
		t.Fatal(s.Err())
	}
	if cfg := s.Get(); !cfg.Enabled || len(cfg.BannedFingerprints) != 0 {
		t.Errorf("default = %+v, want open and empty", cfg)
	}
}

func TestDoorStore_SetPersistsAndReloads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "anon-door.json")
	s := NewDoorStore(path)
	in := DoorConfig{Enabled: false, DisabledMessage: "  closed for maintenance ", BannedFingerprints: []string{"SHA256:b", " SHA256:a ", "SHA256:b", ""}}
	if err := s.Set(in); err != nil {
		t.Fatal(err)
	}
	want := DoorConfig{Enabled: false, DisabledMessage: "closed for maintenance", BannedFingerprints: []string{"SHA256:a", "SHA256:b"}}
	check := func(label string, cfg DoorConfig) {
		t.Helper()
		if cfg.Enabled != want.Enabled || cfg.DisabledMessage != want.DisabledMessage || strings.Join(cfg.BannedFingerprints, ",") != strings.Join(want.BannedFingerprints, ",") {
			t.Errorf("%s = %+v, want %+v", label, cfg, want)
		}
