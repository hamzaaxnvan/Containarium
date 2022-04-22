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
			name:   "all preconditions verified",
			mutate: func(*ContainerNodeFacts) {},
		},
		{
			name: "missing nesting is named",
			mutate: func(f *ContainerNodeFacts) {
				f.Nesting = Probe{State: ProbeMissing, Detail: "incus reports no system-container (lxc) driver"}
			},
			wantErr: []string{"nesting", "lxc"},
		},
		{
			name: "missing cgroup v2 is named",
			mutate: func(f *ContainerNodeFacts) {
				f.CgroupV2 = Probe{State: ProbeMissing, Detail: "/sys/fs/cgroup/cgroup.controllers not found"}
			},
			wantErr: []string{"cgroup v2", "cgroup.controllers"},
		},
		{
			name: "missing br_netfilter is named",
			mutate: func(f *ContainerNodeFacts) {
				f.BrNetfilter = Probe{State: ProbeMissing, Detail: "kernel module br_netfilter not present; load it (modprobe br_netfilter)"}
			},
			wantErr: []string{"br_netfilter", "modprobe"},
		},
		{
			name: "missing overlay is named",
			mutate: func(f *ContainerNodeFacts) {
				f.Overlay = Probe{State: ProbeMissing, Detail: "kernel module overlay not present"}
			},
			wantErr: []string{"overlay"},
		},
		{
			name: "unprobeable precondition refuses as unverified, never assumed green",
			mutate: func(f *ContainerNodeFacts) {
				f.BrNetfilter = Probe{State: ProbeUnknown, Detail: "stat /sys/module/br_netfilter: permission denied"}
			},
			wantErr: []string{"br_netfilter", "could not be verified", "permission denied"},
		},
		{
			name: "every problem is named, not just the first",
			mutate: func(f *ContainerNodeFacts) {
				f.CgroupV2 = Probe{State: ProbeMissing, Detail: "host cgroup hierarchy is not cgroup v2"}
				f.Overlay = Probe{State: ProbeUnknown, Detail: "stat /sys/module/overlay: permission denied"}
			},
			wantErr: []string{"cgroup v2", "overlay", "could not be verified"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := greenFacts()
			tc.mutate(&f)
			err := CheckContainerNodeFacts(f)
			if len(tc.wantErr) == 0 {
				if err != nil {
					t.Fatalf("CheckContainerNodeFacts = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatal("CheckContainerNodeFacts = nil, want a refusal")
			}
			if !errors.Is(err, ErrContainerNodesUnsupported) {
				t.Fatalf("refusal is not ErrContainerNodesUnsupported: %v", err)
			}
			for _, want := range tc.wantErr {
				if !strings.Contains(err.Error(), want) {
