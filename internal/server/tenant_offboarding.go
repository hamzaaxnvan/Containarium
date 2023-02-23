package server

import (
	"context"
	"fmt"
	"log"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/auth"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Tenant offboarding (#1343).
//
// Per-tenant encryption creates two durable resources — an Incus storage pool
// and the encrypted dataset it is sourced at — and until now nothing removed
// them. A tenant who left kept both forever, the dataset holding their
// key-encrypted data indefinitely.
//
// The ORDER is the whole issue. Destroying the dataset first leaves an Incus
// pool pointing at nothing, and EnsureStorage/reviewExistingStorage walk every
// pool at daemon start — so a mis-ordered teardown breaks startup for every
// tenant on the host, not only the departing one.

// DeleteTenantStorage destroys a departing tenant's encrypted storage.
//
// Irreversible: the dataset is the tenant's encryptionroot, so destroying it
// destroys the only key path to their data. Admin only, like every other
// operation on tenant key custody.
func (s *ContainerServer) DeleteTenantStorage(ctx context.Context, req *pb.DeleteTenantStorageRequest) (*pb.DeleteTenantStorageResponse, error) {
	if req.GetTenant() == "" {
		return nil, status.Error(codes.InvalidArgument, "tenant is required")
	}
	if err := auth.RequireRole(ctx, auth.RoleAdmin); err != nil {
		return nil, err
	}

	pool := tenantPoolName(req.GetTenant())
	dataset, err := tenantDataset(s.encryption.tenantRootOrEmpty(), req.GetTenant())
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "resolve the tenant's dataset: %v", err)
	}

	if err := s.encryption.DestroyTenantStorage(ctx, req.GetTenant()); err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "%v", err)
	}

