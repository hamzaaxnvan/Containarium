package server

import (
	"context"
	"testing"
	"time"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func denyCidrs(rules []*pb.NetworkPolicyDenyRule) []string {
	out := make([]string, 0, len(rules))
	for _, r := range rules {
		out = append(out, r.GetCidr())
	}
	return out
}

func TestApplyQuarantine(t *testing.T) {
	exp := time.Date(2026, 6, 13, 0, 0, 0, 0, time.UTC)

	// Empty → adds the quarantine rule with our note + expiry.
	got := applyQuarantine(nil, exp)
	if len(got) != 1 || got[0].GetCidr() != quarantineCIDR || got[0].GetNote() != quarantineNote {
		t.Fatalf("quarantine add wrong: %+v", got)
	}
