package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/protobuf/proto"
	"sort"
)

var ErrTenantAccessConflict = errors.New("network policy changed since preview")

type TenantAccessStore interface {
	PatchTenantAccess(context.Context, []*pb.TenantAccessChange) ([]*pb.TenantAccessPolicy, error)
}

// PolicyRevision covers the entire policy, including independently managed deny
// rules. An ingress patch can never silently discard an intervening policy edit.
func PolicyRevision(p *pb.NetworkPolicy) string {
	if p == nil {
		return incus.TenantAccessAbsentRevision
	}
	b, _ := proto.MarshalOptions{Deterministic: true}.Marshal(p)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func validateAccessChanges(changes []*pb.TenantAccessChange) error {
	if len(changes) == 0 || len(changes) > 32 {
		return fmt.Errorf("1-32 destination changes required")
	}
	seen := map[string]bool{}
	for _, c := range changes {
		if c == nil {
			return fmt.Errorf("invalid destination change")
		}
		if err := incus.ValidateNetworkTenant(c.Destination); err != nil {
			return err
		}
		if seen[c.Destination] {
			return fmt.Errorf("duplicate destination")
		}
		seen[c.Destination] = true
		if c.ExpectedRevision == incus.TenantAccessAbsentRevision && len(c.AddSources) == 0 {
			return fmt.Errorf("no policy exists to revoke; omit this no-op destination")
		}
		if c.ExpectedRevision != incus.TenantAccessAbsentRevision {
			b, err := hex.DecodeString(c.ExpectedRevision)
			if err != nil || len(b) != 32 {
				return fmt.Errorf("expected full policy revision is required")
			}
		}
		if len(c.AddSources)+len(c.RemoveSources) == 0 || len(c.AddSources)+len(c.RemoveSources) > 100 {
			return fmt.Errorf("1-100 ingress source edits required")
		}
		sources := map[string]bool{}
		for _, src := range append(append([]string{}, c.AddSources...), c.RemoveSources...) {
			if err := incus.ValidateNetworkTenant(src); err != nil {
				return err
			}
			if src == c.Destination || sources[src] {
				return fmt.Errorf("self, duplicate or contradictory ingress source")
			}
			sources[src] = true
		}
	}
	return nil
}

func applyAccess(old *pb.NetworkPolicy, c *pb.TenantAccessChange) (*pb.NetworkPolicy, error) {
	if PolicyRevision(old) != c.ExpectedRevision {
		return nil, ErrTenantAccessConflict
	}
	var next *pb.NetworkPolicy
	if old == nil {
		// No pre-existing egress policy is reset. The new record remains log-only;
		// its allow_from_tenants is consumed independently by the NIC guard.
		next = &pb.NetworkPolicy{Tenant: c.Destination}
	} else {
		next = proto.Clone(old).(*pb.NetworkPolicy)
	}
	allowed := map[string]bool{}
	for _, src := range next.AllowFromTenants {
		allowed[src] = true
	}
	for _, src := range c.RemoveSources {
		delete(allowed, src)
	}
	for _, src := range c.AddSources {
