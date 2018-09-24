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
