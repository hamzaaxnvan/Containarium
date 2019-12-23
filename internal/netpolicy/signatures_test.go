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

	bad := []struct {
		name string
		sig  *pb.NetworkPolicySignature
		want string
	}{
		{"nil", nil, "nil"},
		{"empty name", &pb.NetworkPolicySignature{Pattern: "x"}, "name is required"},
		{"slash in name", &pb.NetworkPolicySignature{Name: "a/b", Pattern: "x"}, "must not contain"},
		{"space in name", &pb.NetworkPolicySignature{Name: "a b", Pattern: "x"}, "must not contain"},
		{"empty pattern", &pb.NetworkPolicySignature{Name: "ok"}, "pattern is required"},
		{"long pattern", &pb.NetworkPolicySignature{Name: "ok", Pattern: strings.Repeat("A", sigMaxLen+1)}, "max"},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			if _, _, err := ValidateSignature(c.sig); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("want error containing %q, got %v", c.want, err)
			}
		})
	}
}

func TestBuiltinSignatures(t *testing.T) {
	sigs := BuiltinSignatures()
	if len(sigs) == 0 {
		t.Fatal("no built-in signatures")
