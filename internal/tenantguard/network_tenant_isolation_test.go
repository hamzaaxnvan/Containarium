package tenantguard

import (
	"context"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"testing"
)

func TestTenantReviewGuardMustSeparateAccountFromSharedTenant(t *testing.T) {
	admin := incus.ContainerInfo{Name: "admin-box-container", Tenant: "team-a", IPAddress: "10.100.0.18", State: "Running"}
	user := incus.ContainerInfo{Name: "team-a-container", Tenant: "account:team-a", IPAddress: "10.100.0.19", State: "Running"}
	f := newFakeBackend("nftables", admin, user)
	r := NewReconciler(f, enforceCfg())
