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
