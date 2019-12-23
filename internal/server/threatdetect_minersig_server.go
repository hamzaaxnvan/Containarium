package server

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/threatdetect"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// List/Add/RemoveMinerSignature (#2638) — the miner-signature list the
// miner-signature rule matches execs against. Same gates as the
// bad-destination verbs: reading needs security:read; mutating needs
// security:write AND the admin role, because the list protects the whole
// fleet and removing an entry silently disables a detection.

// SetMinerSignatureRule wires the rule. nil (construction failed) makes the
// handlers report Unavailable.
func (s *ThreatDetectionServer) SetMinerSignatureRule(r *threatdetect.MinerSignatureRule) {
	s.minerSigRule = r
}

// ListMinerSignatures returns baseline then operator entries.
func (s *ThreatDetectionServer) ListMinerSignatures(ctx context.Context, _ *pb.ListMinerSignaturesRequest) (*pb.ListMinerSignaturesResponse, error) {
	if err := auth.RequireScope(ctx, auth.ScopeSecurityRead); err != nil {
		return nil, err
	}
	if s.minerSigRule == nil {
		return nil, status.Errorf(codes.Unavailable, "miner-signature rule not available")
	}
	entries := s.minerSigRule.ListSignatures()
	out := make([]*pb.MinerSignature, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.ToProto())
	}
	return &pb.ListMinerSignaturesResponse{Signatures: out, BaselineVersion: s.minerSigRule.BaselineVersion()}, nil
}

// AddMinerSignature adds an operator entry, effective immediately.
func (s *ThreatDetectionServer) AddMinerSignature(ctx context.Context, req *pb.AddMinerSignatureRequest) (*pb.AddMinerSignatureResponse, error) {
	if err := auth.RequireScope(ctx, auth.ScopeSecurityWrite); err != nil {
		return nil, err
	}
	if err := auth.RequireRole(ctx, auth.RoleAdmin); err != nil {
		return nil, err
