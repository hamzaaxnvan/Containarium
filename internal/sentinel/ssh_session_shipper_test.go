package sentinel

import (
	"path/filepath"
	"sort"
	"testing"
)

// #2415 — wiring the SSH session shipper into the sentinel process.

func TestFileAuditIngestTokens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.json")
	src := FileAuditIngestTokens{Path: path}

	if bs, err := src.Backends(); err != nil || len(bs) != 0 {
		t.Fatalf("no file: backends=%v err=%v", bs, err)
	}
	if _, ok, err := src.TokenFor("b1"); ok || err != nil {
		t.Fatalf("no file: ok=%v err=%v", ok, err)
	}

