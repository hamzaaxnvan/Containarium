package k8s

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// These guard the two pins in scripts/k8s-e2e.sh that decide what the kind
// e2e is actually evidence *about*. Both have already failed in ways that
// cost a CI cluster build to discover, and both are checkable offline.
//
// They read the script as text on purpose: the point is to catch a bad pin
// before CI spends ~90s building a cluster to find out, so the check has to
// be cheaper than running the thing it guards.

const (
	repoRoot     = "../../../.."
	e2eScript    = repoRoot + "/scripts/k8s-e2e.sh"
	goModPath    = repoRoot + "/go.mod"
	agentSandbox = "sigs.k8s.io/agent-sandbox"
)

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// scriptSandboxVersion extracts the AGENT_SANDBOX_VERSION default.
func scriptSandboxVersion(t *testing.T, script string) string {
	t.Helper()
	m := regexp.MustCompile(`AGENT_SANDBOX_VERSION="\$\{AGENT_SANDBOX_VERSION:-(v[0-9][^}]*)\}"`).FindStringSubmatch(script)
	if m == nil {
		t.Fatal("could not find the AGENT_SANDBOX_VERSION default in scripts/k8s-e2e.sh")
	}
	return m[1]
}

// The e2e is only evidence about the controller we build against. Pinned to
// an older release it silently exercises older behaviour — which is exactly
// what happened: the script sat at v0.5.1 while go.mod moved to v0.5.4, the
