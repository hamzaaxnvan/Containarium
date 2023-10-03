package k8s

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestHelmChartSshpiperLabelMatchesNetworkPolicyContract renders the Helm
// chart's sshpiper Deployment and Service and asserts their pod-selecting
// label is exactly sshpiperNameLabel=sshpiper — the contract
// networkPolicyObject's ingress rule (networkpolicy_test.go) hardcodes and
// the comment on sshpiperNameLabel documents ("A gateway deployed by other
// means MUST carry this label or the ingress rule below will not admit
// it."). The chart previously used a chart-name-prefixed label instead
// (containarium-k8s-sshpiper), which silently made every Helm-deployed
// gateway invisible to the default-deny NetworkPolicy's ingress allowance —
// on any NetworkPolicy-enforcing CNI, sshpiper could never reach a box (#1492).
//
// Skips if helm isn't on PATH rather than failing: this guards a real
// regression when it can run, without making helm a hard dependency of
// `go test ./...` for contributors who don't have it installed.
func TestHelmChartSshpiperLabelMatchesNetworkPolicyContract(t *testing.T) {
	helmPath, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm not found on PATH, skipping chart-rendering test")
	}

	out, err := exec.Command(helmPath, "template", "test-release", "../../../../charts/containarium-k8s",
		"--set", "daemon.jwtSecret=deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdead",
		// gateway.upstreamKeySecret must be set or the chart's own #1496 guard
		// (daemon-deployment.yaml) refuses to render at all — unrelated to what
		// this test checks, but required to get any output back.
		"--set", "gateway.upstreamKeySecret=sshpiper-upstream-key",
		"--show-only", "templates/sshpiper-deployment.yaml",
		"--show-only", "templates/sshpiper-service.yaml",
	).CombinedOutput()
