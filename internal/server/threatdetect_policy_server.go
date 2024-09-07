package server

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/threatdetect"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Get/SetProcessPolicy and ReleaseProcessAction (#2636) — the process
// guardrail's policy and release RPCs on ThreatDetectionServer. Kept in their
// own file: the status/findings/bad-destination handlers in
// threatdetect_server.go are shared with sibling stories.

// SetProcessPolicyStore wires the policy store. Independent of whether the
// sentry is armed — an operator sets the policy before flipping
// CONTAINARIUM_THREAT_SENTRY on, the way the bad-destination list works.
// nil is valid (no store); the two policy handlers report Unavailable.
func (s *ThreatDetectionServer) SetProcessPolicyStore(store *threatdetect.ProcessPolicyStore) {
	s.policies = store
}

// SetMiningThresholdsConfig wires the mining-behavior thresholds the rule
// reads, so `process-policy set` can tune them (ProcessPolicy.mining_behavior).
func (s *ThreatDetectionServer) SetMiningThresholdsConfig(c *threatdetect.MiningThresholdsConfig) {
	s.miningCfg = c
}

// withEffectiveThresholds returns a copy of p carrying the mining-behavior
// thresholds the rule is actually using (stored values over built-in
// defaults), so a read never shows them empty even when nothing was set.
func (s *ThreatDetectionServer) withEffectiveThresholds(p *pb.ProcessPolicy) *pb.ProcessPolicy {
	if s.miningCfg == nil {
		return p
	}
	out := &pb.ProcessPolicy{}
	if p != nil {
		out = proto.Clone(p).(*pb.ProcessPolicy)
	}
	t := s.miningCfg.Effective()
	out.MiningBehavior = &pb.MiningBehaviorThresholds{
		MinCores:           t.MinCores,
		MinFractionOfLimit: t.MinFractionOfLimit,
		Window:             durationpb.New(t.Window),
		FlowMinAge:         durationpb.New(t.FlowMinAge),
		StratumPorts:       t.StratumPorts,
	}
	return out
}

