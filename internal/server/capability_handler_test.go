//go:build !containarium_client

package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/capabilities"
	"github.com/footprintai/containarium/pkg/core/container"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// capabilityTestResources supplies complete host facts without a live Incus daemon.
func capabilityTestResources() (*incus.SystemResources, error) {
	return &incus.SystemResources{TotalCPUs: 8, TotalMemoryBytes: 16 << 30, TotalDiskBytes: 100 << 30}, nil
}

// TestCapabilityResourceDiscoveryFailure checks automatic failure and explicit retry.
func TestCapabilityResourceDiscoveryFailure(t *testing.T) {
	for _, failure := range []string{"error", "empty", "zero CPU", "zero memory"} {
		t.Run(failure, func(t *testing.T) {
			srv := &ContainerServer{capabilityResources: func() (*incus.SystemResources, error) {
				switch failure {
				case "error":
					return nil, errors.New("resource lookup failed")
				case "empty":
					return nil, nil
				case "zero CPU":
					return &incus.SystemResources{TotalMemoryBytes: 16 << 30}, nil
				default:
					return &incus.SystemResources{TotalCPUs: 8}, nil
				}
			}}
			ds := &DualServer{config: &DualServerConfig{Pool: "test-pool"}, containerServer: srv}
			<-ds.startCapabilityProfile(context.Background())
			if _, ok := srv.capabStore().Current(); ok {
				t.Fatal("resource discovery failure must not record a profile")
			}
			ctx := auth.ContextWithSystemIdentity(context.Background())
			if _, err := srv.ProfileBackend(ctx, &pb.ProfileBackendRequest{SkipGpu: true}); status.Code(err) != codes.Internal {
				t.Fatalf("explicit profile must reject unavailable resources: %v", err)
			}
			srv.capabilityResources = capabilityTestResources
			got, err := srv.ProfileBackend(ctx, &pb.ProfileBackendRequest{SkipGpu: true})
			if err != nil || got.Profile == nil || got.Profile.CpuCores != 8 || got.Profile.TotalMemoryBytes != 16<<30 {
				t.Fatalf("explicit retry must record real host facts: %v, %v", got, err)
			}
		})
	}
}

// TestPoolMemberAutomaticallyProfiles checks that a pool member records a queryable profile without an RPC.
func TestPoolMemberAutomaticallyProfiles(t *testing.T) {
	srv := &ContainerServer{capabilityResources: func() (*incus.SystemResources, error) {
		return &incus.SystemResources{TotalCPUs: 8, TotalMemoryBytes: 16 << 30}, nil
	}}
	srv.SetCapabilityIdentity("region-a", "cpu-small")
	ds := &DualServer{config: &DualServerConfig{Pool: "cpu-small"}, containerServer: srv}
	done := ds.startCapabilityProfile(context.Background())
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("automatic profile did not finish")
	}
	got, err := srv.GetCapabilityProfile(auth.ContextWithSystemIdentity(context.Background()), &pb.GetCapabilityProfileRequest{})
