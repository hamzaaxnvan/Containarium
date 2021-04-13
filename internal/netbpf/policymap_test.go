package netbpf

import (
	"net/netip"
	"testing"

	"github.com/footprintai/containarium/internal/netpolicy"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func mustCompile(t *testing.T, p *pb.NetworkPolicy) netpolicy.CompiledPolicy {
	t.Helper()
	c, err := netpolicy.Compile(p)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return c
}

func TestCompileConfig(t *testing.T) {
	c := mustCompile(t, &pb.NetworkPolicy{
		Tenant:           "alice",
		AllowIntraTenant: true,
		Mode:             pb.NetworkPolicyMode_NETWORK_POLICY_MODE_ENFORCE,
	})
	got := CompileConfig(42, c)
	want := PolicyConfig{TenantID: 42, Mode: ModeEnforce, AllowIntra: 1, EgressMode: ModeEnforce} // unset egress_mode follows mode
	if got != want {
		t.Errorf("CompileConfig() = %+v, want %+v", got, want)
	}
}

func TestCompileConfig_Metadata(t *testing.T) {
