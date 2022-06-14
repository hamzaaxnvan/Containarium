package sentinel

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadTunnelTokenStore_MissingFileIsNotError(t *testing.T) {
	entries, err := LoadTunnelTokenStore(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil {
		t.Fatalf("missing file must not be an error: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no entries, got %d", len(entries))
	}
}

func TestSaveTunnelTokenStore_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tunnel-tokens.json")
	want := []TunnelTokenEntry{
		{Token: "tok-a", Pools: []Pool{PoolAny}},
		{Token: "tok-b", Pools: []Pool{"lab", "prod"}},
	}
	if err := SaveTunnelTokenStore(path, want); err != nil {
		t.Fatalf("SaveTunnelTokenStore: %v", err)
	}
	got, err := LoadTunnelTokenStore(path)
	if err != nil {
		t.Fatalf("LoadTunnelTokenStore: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("entry count = %d, want %d", len(got), len(want))
	}
	for i := range want {
