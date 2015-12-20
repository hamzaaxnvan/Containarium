package server

import (
	"github.com/footprintai/containarium/pkg/core/container"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"testing"
	"time"
)

func TestTenantReviewLegacyCreateIgnoresUnavailableTenantDependencies(t *testing.T) {
	t.Setenv("CONTAINARIUM_NATIVE_ACCOUNT_TENANCY", "off")
	calls := 0
	s := &ContainerServer{manager: &container.Manager{}, nativeTenantReady: func() bool { calls++; return false }}
	req := &pb.CreateContainerRequest{Username: "workload", DryRun: true}
	out, err := s.CreateContainer(nativeUser(), req)
	if err != nil || !out.GetDryRun() || calls != 0 || req.NetworkTenant != "" {
		t.Fatalf("out=%v err=%v readinessCalls=%d requestTenant=%q", out, err, calls, req.NetworkTenant)
	}
}
func TestTenantReviewAccountNamespaceMustNotJoinSharedDomain(t *testing.T) {
	req := &pb.CreateContainerRequest{Username: "team-a"}
	if err := authorizeNetworkTenantCreate(nativeUser(), req, true, true); err != nil {
		t.Fatal(err)
	}
	actual := incus.ResolveTenant(req.NetworkTenant, "", "team-a-container")
	shared := incus.ResolveTenant("team-a", "", "admin-box-container")
	if actual == shared {
		t.Fatalf("account %q and admin shared tenant resolve to same Guard domain %q", req.NetworkTenant, actual)
	}
}
func TestTenantReviewLocalPlacementMustRemainSupported(t *testing.T) {
	s := &ContainerServer{peerPool: NewPeerPool("local-fixture", "", nil, "local-pool")}
	for _, req := range []*pb.CreateContainerRequest{{Username: "workload", BackendId: "local-fixture"}, {Username: "workload", BackendId: "local-fixture", Pool: "local-pool"}} {
