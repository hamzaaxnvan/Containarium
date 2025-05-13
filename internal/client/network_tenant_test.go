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
