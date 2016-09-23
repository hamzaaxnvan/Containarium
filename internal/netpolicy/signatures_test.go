package netpolicy

import (
	"strings"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Mirror the BPF program's compile-time limits (netbpf.SigMaxLen/SigMaxCount);
// the netpolicy package can't import netbpf (that would cycle), so they're
// duplicated here and a violation fails the build's tests.
const (
	sigMaxLen   = 32
	sigMaxCount = 32
)

func TestValidateSignature(t *testing.T) {
	// OK: trimmed name + pattern bytes returned.
	name, pat, err := ValidateSignature(&pb.NetworkPolicySignature{Name: "  CVE-2024-1 ", Pattern: "${jndi:"})
	if err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
	if name != "CVE-2024-1" || string(pat) != "${jndi:" {
		t.Fatalf("normalized wrong: name=%q pat=%q", name, pat)
	}
