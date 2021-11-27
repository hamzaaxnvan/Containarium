package cluster

// Truthful node capacity for container-class nodes, and the containerd
// configuration that lets pods run in them at all (design Amendment 1
// in docs/architecture/cluster-container-node-pools.md, #1439).
//
// Both exist because a container node is not the machine Kubernetes
// thinks it is: containerd's CRI plugin writes a sysctl an unprivileged
// container may not touch, and cadvisor reads the OUTER host's /proc,
// so a 2-cpu node cheerfully advertises the host's 8. The first breaks
// every pod; the second breaks autoscaling silently, which is worse.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// ContainerdConfigTemplatePath is where k3s reads a containerd config
// template from. containerd config version 3 (k3s 1.33+) uses the
// -v3 name; the older name is ignored by these releases.
const ContainerdConfigTemplatePath = "/var/lib/rancher/k3s/agent/etc/containerd/config-v3.toml.tmpl"

// ContainerdGeneratedConfigPath is where k3s writes the containerd
// config it generates on start. On container-class nodes the bootstrap
// derives the config template FROM this file — a hand-written template
// cannot safely extend it, because k3s's base template already
// declares [plugins.'io.containerd.cri.v1.runtime'] and re-declaring a
// TOML table is invalid, so containerd refuses the whole config and
// k3s never starts (#1444).
const ContainerdGeneratedConfigPath = "/var/lib/rancher/k3s/agent/etc/containerd/config.toml"

// containerdUnprivilegedToggles are the CRI plugin settings that make
// runc write sysctls (net.ipv4.ip_unprivileged_port_start) an
// unprivileged container may not touch. With either enabled every pod
// sandbox dies:
//
//	runc create failed: ... open sysctl net.ipv4.ip_unprivileged_port_start:
//	  reopen fd 8: permission denied
//
// Flipping both to false was verified live on a nested host: node
// Ready, system pods Running, no privileged flag anywhere (design
// Amendment 1). The cost is real and documented: pods on
// container-class nodes cannot bind ports below 1024 without
// CAP_NET_BIND_SERVICE, and unprivileged ICMP is unavailable.
// VM-class nodes are unaffected — they never get the derivation.
var containerdUnprivilegedToggles = []string{
	"enable_unprivileged_ports",
	"enable_unprivileged_icmp",
}

// ContainerdDeriveTemplateSedArgs returns the sed arguments (sans the
// input file) that turn k3s's generated containerd config into the
// config template for container-class nodes: an exact copy with the
// two unprivileged toggles flipped to false. Exported so the tests
// run the very same derivation the bootstrap embeds.
func ContainerdDeriveTemplateSedArgs() []string {
	var args []string
	for _, key := range containerdUnprivilegedToggles {
		args = append(args, "-e", fmt.Sprintf("s/%s = true/%s = false/", key, key))
	}
	return args
}
