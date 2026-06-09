package cluster

// Container node runtime (#1429): the profile (the ONE place holding
// the k3s-in-container knowledge) and the ContainerNodeCapable probe.
// Design: docs/architecture/cluster-container-node-pools.md.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// --- the profile ------------------------------------------------------

// The applied Incus config set is golden-pinned: what a tenant's node
// container is granted changes only as a reviewable golden diff.
func TestContainerNodeConfigGolden(t *testing.T) {
	cfg := ContainerNodeConfig()
	keys := make([]string, 0, len(cfg))
	for k := range cfg {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%s\n", k, cfg[k])
	}
	golden(t, "container-node-config.golden", b.String())
}

func TestContainerNodeConfigHardLines(t *testing.T) {
	cfg := ContainerNodeConfig()
	if cfg["security.nesting"] != "true" {
		t.Fatalf("security.nesting = %q, want \"true\" (containerd runs pods inside the node)", cfg["security.nesting"])
	}
	// The design's hard line: NO security.privileged, ever. A future
	// k8s feature that seems to need it is a new design conversation,
	// not a config tweak — this test is the tripwire.
	if v, ok := cfg["security.privileged"]; ok {
		t.Fatalf("container node profile sets security.privileged=%q — forbidden by design", v)
	}
}

// --- probe verdict: CheckContainerNodeFacts ---------------------------

func greenFacts() ContainerNodeFacts {
	return ContainerNodeFacts{
		Nesting:     Probe{State: ProbeOK},
		CgroupV2:    Probe{State: ProbeOK},
		BrNetfilter: Probe{State: ProbeOK},
		Overlay:     Probe{State: ProbeOK},
	}
}

func TestCheckContainerNodeFacts(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*ContainerNodeFacts)
		wantErr []string // substrings the refusal must name; nil = capable
	}{
		{
