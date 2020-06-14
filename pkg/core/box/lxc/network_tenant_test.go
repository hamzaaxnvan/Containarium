package lxc

import (
	"github.com/footprintai/containarium/pkg/core/box"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"testing"
)

func TestNativeTenantSpecAndProviderReadback(t *testing.T) {
	opts := specToCreateOptions(box.BoxSpec{Ref: box.BoxRef{Tenant: "workload"}, NetworkTenant: "team-a"})
	if opts.Username != "workload" || opts.NetworkTenant != "team-a" {
		t.Fatal(opts)
	}
