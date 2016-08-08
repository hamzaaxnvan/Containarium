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

	if idx := strings.Index(makefile, "\nCADDY_VERSION="); idx >= 0 {
		line := makefile[idx+1:]
		if nl := strings.IndexByte(line, '\n'); nl >= 0 {
			line = line[:nl]
		}
		makefileVersion := strings.TrimPrefix(line, "CADDY_VERSION=")
		if makefileVersion != CaddyVersion {
			t.Errorf("Makefile's CADDY_VERSION=%s does not match app.CaddyVersion=%s", makefileVersion, CaddyVersion)
		}
	} else {
		t.Error("Makefile has no CADDY_VERSION= line — did the pin get renamed or removed?")
	}

	target := extractMakeTarget(makefile, "build-caddy:")
	if target == "" {
		t.Fatal("Makefile has no build-caddy: target — did it get renamed or removed?")
	}

	for _, mod := range AllDNSProviderModules() {
		if !strings.Contains(target, mod) {
			t.Errorf("Makefile's build-caddy target is missing --with %s (present in dnsProviderModules but not baked into the release binary)", mod)
		}
	}

	// Catch drift in the other direction too: a module the Makefile still
	// lists but that was removed from dnsProviderModules would silently keep
