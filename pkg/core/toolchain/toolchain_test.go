package toolchain

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNodeRuntimeLibrariesAreProvisionedOnlyWhenNeeded(t *testing.T) {
	root, commands := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "current/bin"), 0755); err != nil {
		t.Fatal(err)
	}
	marker, log := filepath.Join(root, "ready"), filepath.Join(root, "installs")
	node := fmt.Sprintf("#!/bin/sh\n[ -f '%s' ]\n", marker)
	if err := os.WriteFile(filepath.Join(root, "current/bin/node"), []byte(node), 0755); err != nil {
		t.Fatal(err)
	}
	apt := fmt.Sprintf("#!/bin/sh\necho \"$*\" >> '%s'\nif [ \"$1\" = install ]; then touch '%s'; fi\n", log, marker)
	if err := os.WriteFile(filepath.Join(commands, "apt-get"), []byte(apt), 0755); err != nil {
		t.Fatal(err)
