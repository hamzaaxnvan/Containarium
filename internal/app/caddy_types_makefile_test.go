package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCaddyModulesInSyncWithMakefile guards the one place #1617's pre-built
// Caddy release asset can silently drift from dnsProviderModules: the
// Makefile's build-caddy target hardcodes its own `--with` list (make has
// no clean way to call back into this package), so nothing else catches a
// provider added to one but not the other — the release binary would ship
// missing a module, and the gap would only surface as a live "unknown
// module" failure the way #1617 itself was filed after (Containarium-cloud
// #1351).
func TestCaddyModulesInSyncWithMakefile(t *testing.T) {
	root, err := repoRootForCaddyMakefileTest()
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	makefile := string(data)
