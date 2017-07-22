package k8s

import (
	"context"
	"encoding/base64"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// sshpiperRouter is the sshpiper-backed gatewayRouter: it programs one Pipe CR
// per tenant in the gateway namespace, which the sshpiper Kubernetes plugin
// watches to route incoming SSH by username to the tenant's box pod. It is the
// only gatewayRouter implementation today; the seam (gateway.go) exists so a
// different SSH front end could replace it without touching box lifecycle.
type sshpiperRouter struct{ b *Backend }

var _ gatewayRouter = (*sshpiperRouter)(nil)

// pipeGVR is the sshpiper Kubernetes plugin's CRD. The design note
// (docs/K8S-AGENT-BOX-RUNTIME-DESIGN.md) calls it "PiperUpstream"; the
// maintained plugin's actual resource is `pipes` in group sshpiper.com/v1beta1
// (kind Pipe). sshpiper watches a namespace for these and routes incoming SSH
// by username — so programming a Pipe is how the gateway learns to forward a
// tenant's connection to its box pod.
var pipeGVR = schema.GroupVersionResource{Group: "sshpiper.com", Version: "v1beta1", Resource: "pipes"}

func pipeName(tenant string) string { return "box-" + tenant }

// Enabled reports whether SSH-gateway routing should be programmed: a dynamic
// client plus a configured gateway namespace where Pipes live. When off (no
// GatewayNamespace), boxes are still reconciled but not routed — useful for
// clusters without sshpiper, and for the core lifecycle e2e.
func (r *sshpiperRouter) Enabled() bool {
	return r.b.dyn != nil && r.b.cfg.GatewayNamespace != ""
}

// upstreamHost is the in-cluster DNS the gateway forwards the tenant's SSH to:
// the Sandbox's controller-created headless Service (named after the Sandbox),
// whose A record resolves to the box pod. Matches the Sandbox's
// status.serviceFQDN; computed here rather than read from status so the Pipe
// can be programmed at Create time, before the controller first reconciles.
func (r *sshpiperRouter) upstreamHost(tenant string) string {
	return fmt.Sprintf("%s.%s.svc.cluster.local:%d", sandboxName, r.b.namespaceFor(tenant), sshPort)
}

// pipeObject builds the sshpiper Pipe that routes username=<tenant> to the
// tenant's box pod: the incoming connection authenticates against the box's
// authorized keys (inline, base64), and the upstream host key is trusted
// (ignore_hostkey — TOFU/known_hosts pinning is a follow-up).
//
// fromKeys is the union of the agent's client keys (direct node-gateway
// access) and any authorized sentinel keys (hop 2 of the sentinel chain) —
// both authenticate the incoming connection at the node gateway.
func (r *sshpiperRouter) pipeObject(tenant string, fromKeys []string, hostPubKeys []string) *unstructured.Unstructured {
	var buf []byte
	for _, k := range fromKeys {
		buf = append(buf, []byte(k)...)
		buf = append(buf, '\n')
	}
	to := map[string]any{
		"host":     r.upstreamHost(tenant),
		"username": boxSSHUser, // fixed box login user; tenant identity is enforced by from.username
	}
	// Host-key handling: pin the box's host keys (known_hosts_data) when we have
	// them, else fall back to ignore_hostkey (the escape hatch / pre-pinning
	// behavior). Both keys are pinned because sshpiper may negotiate either —
	// stops a man-in-the-middle between sshpiper and the box.
	if len(hostPubKeys) > 0 && !r.b.cfg.InsecureIgnoreHostKey {
		to["known_hosts_data"] = knownHostsData(r.upstreamHost(tenant), hostPubKeys...)
	} else {
