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

	if err := SaveAuditIngestTokenStore(path, []AuditIngestTokenEntry{{BackendID: "b1", Token: "t1"}, {BackendID: "b2", Token: "t2"}}); err != nil {
		t.Fatal(err)
	}
	if tok, ok, err := src.TokenFor("b2"); !ok || err != nil || tok != "t2" {
		t.Fatalf("TokenFor(b2) = %q,%v,%v", tok, ok, err)
	}
	if _, ok, _ := src.TokenFor("absent"); ok {
		t.Fatal("absent backend must report !ok")
	}
	bs, _ := src.Backends()
	sort.Strings(bs)
