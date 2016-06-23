package sdlcskills

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #2488: the daemon reads the same sdlc-skills.tar.gz the boxes get, verified
// against the embedded pin, and takes one section of one skill from it.

const implementMD = `---
name: "Engineer: Implement"
description: Act as a software engineer.
---

Act as a **software engineer** implementing one scoped task.

**Engineering principles**

- **Test-first** — write the failing test first
- **Small and scoped** — the issue and nothing else

**Test environment**

1. Provision a fresh box over the platform MCP.
`

func tarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	for name, body := range files {
		h := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(body))}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	if _, err := gz.Write(raw.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func lockFor(data []byte) Lock {
	sum := sha256.Sum256(data)
	return Lock{Repo: "example/skills", Ref: strings.Repeat("a", 40), SHA256: hex.EncodeToString(sum[:])}
}

func TestLoadBundleBytes_VerifiesDigestBeforeReading(t *testing.T) {
	data := tarGz(t, map[string]string{"engineer-implement/SKILL.md": implementMD})

	b, err := LoadBundleBytes(data, lockFor(data))
	if err != nil {
		t.Fatalf("matching digest: %v", err)
	}
	if b.Pin() != "example/skills@aaaaaaaaaaaa" {
