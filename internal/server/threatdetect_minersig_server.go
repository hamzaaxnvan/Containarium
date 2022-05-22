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
