package server

import (
	"context"
	"errors"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/pkg/core/zfskey"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Wiring the encrypted create (#1341): CreateContainer resolves the tenant's
// storage before provisioning anything, places the container on it, and
// records where it went.
//
// These pin the wiring and its refusals. Whether the resulting container is
// actually encrypted is a property ZFS computes, asserted on the incus lane.

// encryptedTenantOf is the decision about WHOSE key a container is encrypted
// with. Getting it wrong does not fail — it silently puts two tenants under
// one encryptionroot, which is the boundary the whole feature exists to draw.
func TestEncryptionTenantFor(t *testing.T) {
	for _, tc := range []struct {
		name    string
		req     *pb.CreateContainerRequest
		want    string
		wantWhy string
	}{
		{
			name: "the username is the tenant on an OSS daemon",
			req:  &pb.CreateContainerRequest{Username: "alice"},
			want: "alice",
			wantWhy: "validateTenantID forbids any real tenant_id here, so the username is the " +
				"only tenancy this build has",
		},
		{
			// "default" is what a single-tenant daemon accepts as a synonym
			// for unset. Treating it as the key scope would put EVERY user on
			// one encryptionroot — no isolation at all, silently.
			name:    "tenant_id=default is not a tenant",
			req:     &pb.CreateContainerRequest{Username: "alice", TenantId: "default"},
			want:    "alice",
			wantWhy: "every user would share one key",
		},
		{
			// The cloud build's org-scoped tenancy: a real tenant_id wins,
			// because a tenant's several users must share one encryptionroot.
			name:    "a real tenant_id wins",
			req:     &pb.CreateContainerRequest{Username: "alice", TenantId: "org-acme"},
			want:    "org-acme",
			wantWhy: "one org's containers share one key",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := encryptionTenantFor(tc.req); got != tc.want {
				t.Errorf("encryptionTenantFor = %q, want %q — otherwise %s", got, tc.want, tc.wantWhy)
			}
		})
	}
}

