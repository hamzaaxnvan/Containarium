package server

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/runlease"
	"github.com/footprintai/containarium/pkg/core/skills"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// #2100: RunCrew used to discard every member's lease, so a crew run's boxes
// kept a live gateway token and their fetched checkout of the customer's repo
// after the run reached a terminal state. These tests pin the three terminal
// paths — driveCrew success, driveCrew failure, and a mid-loop provisioning
// failure — each of which must end EVERY lease the run issued.
//
// What they assert is that CrewServer ends the right leases at the right
// moments, not that ending one works: endRunLease/runlease.End have their own
// coverage (agent_run_lease_test.go, internal/runlease/lease_test.go). The
// evidence per member is the revocation of both its credentials, the `rm -rf`
// of its seed dir + workspace in ITS box, and one agent.run_lease_end audit
// row — the crew-side half of the audit trail the issue asks for.

// crewMemberLease is the lease provisionSkillBox hands back for one crew
// member: the CREW RUN's id (every member shares it, #1817) and therefore the
// same per-run seed dir and workspace, but that member's own box, and jtis
// carrying the skill id so a test can tell whose credentials were revoked.
func crewMemberLease(runID, skillID string) runlease.Lease {
	exp := time.Now().Add(agentTokenTTL)
	return runlease.Lease{
		RunID:     runID,
		Box:       "agent-" + skillID + "-container",
		SeedDir:   seedDirFor(runID),
		Workspace: workspaceDirFor(runID),
		Credentials: []runlease.Credential{
			{Kind: runlease.KindPlatformJWT, JTI: "jti-platform-" + skillID, ExpiresAt: exp},
			{Kind: runlease.KindGatewayToken, JTI: "jti-gateway-" + skillID, ExpiresAt: exp},
		},
	}
}

// fakeCrewProvisioner stands in for provisionSkillBox + startServeMode. It
// exists because the real pair is unreachable from a unit test: the seed exec
// ALWAYS fails against a fake container backend ((*container.Manager).Exec
// type-asserts its backend to the concrete *incus.Client), so no fake can
// drive RunCrew past its first member.
type fakeCrewProvisioner struct {
	failOn   string   // skill id whose provisioning fails ("" = none fail)
	failErr  error    // what that failure is; nil = the seed failure below
	provided []string // skill ids provisioned successfully, in order
	leases   []runlease.Lease
}

func (p *fakeCrewProvisioner) provision(_ context.Context, skill *pb.AgentSkill, _ *pb.RunCrewRequest, runID string) (runlease.Lease, string, error) {
	if skill.Id == p.failOn {
		if p.failErr != nil {
			return runlease.Lease{}, "", p.failErr
		}
		// Shaped like the real seed failure (see provisionSkillBoxWith).
		return runlease.Lease{}, "", status.Errorf(codes.Internal, "failed to seed agent box agent-%s-container: exec unsupported", skill.Id)
	}
	lease := crewMemberLease(runID, skill.Id)
	p.provided = append(p.provided, skill.Id)
	p.leases = append(p.leases, lease)
