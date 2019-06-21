package client

import (
	"encoding/json"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNativeTenantCreateRequestIndependentEncryptionAndLegacyFields(t *testing.T) {
	req := NewCreateContainerRequest("workload", "image", "1", "1GB", "1GB", nil, false, "", nil, 0, 0, false, "", "", GitSourceOpts{}, 0, 0, false, 0, "", EncryptionOpts{TenantID: "default"}, "", "", "", nil, "team-a")
	if req.NetworkTenant != "team-a" || req.TenantId != "default" || req.DryRun {
		t.Fatal(req)
	}
	if req.ProtoReflect().Descriptor().Fields().ByName("dry_run").Number() != 29 || req.ProtoReflect().Descriptor().Fields().ByName("network_tenant").Number() != 30 {
		t.Fatal("existing field reused")
	}
	c := &pb.Container{}
	if c.ProtoReflect().Descriptor().Fields().ByName("tool_access").Number() != 30 || c.ProtoReflect().Descriptor().Fields().ByName("network_tenant").Number() != 31 {
		t.Fatal("existing Container field reused")
	}
}
func TestNativeTenantHTTPCreateAndOldProviderNoWrite(t *testing.T) {
	for _, supported := range []bool{true, false} {
		creates := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/network-tenants/capabilities" {
				if !supported {
					http.NotFound(w, r)
					return
				}
