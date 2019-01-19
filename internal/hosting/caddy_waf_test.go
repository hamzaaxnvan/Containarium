package hosting

import (
	"strings"
	"testing"
)

func TestXcaddyBuildArgs(t *testing.T) {
	// Without WAF: provider + caddy-l4 only, no coraza.
	noWAF := strings.Join(xcaddyBuildArgs("github.com/caddy-dns/cloudflare", false, "/usr/local/bin/caddy"), " ")
	if strings.Contains(noWAF, "coraza") {
		t.Fatalf("default build must NOT pull coraza: %s", noWAF)
	}
	if !strings.Contains(noWAF, "caddy-l4") || !strings.Contains(noWAF, "cloudflare") {
		t.Fatalf("build missing base modules: %s", noWAF)
	}

	// With WAF: coraza module added.
