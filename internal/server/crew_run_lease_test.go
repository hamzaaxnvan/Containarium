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
	return lease, "commit-" + skill.Id, nil
}

// crewLeaseHarness is a CrewServer whose lease-ending is fully observable:
// a fake revocation store, a fake wiper, a fake audit logger, and a fake
// provisioner — no container backend anywhere.
type crewLeaseHarness struct {
	crew        *CrewServer
	provisioner *fakeCrewProvisioner
	revocations *fakeRevocationStore
	wiper       *fakeSeedWiper
	audits      *fakeAuditLogger
}

// newCrewLeaseHarness wires a one-crew PIPELINE catalog over skillIDs (which
// must be really wired relay-agent -> hello-agent style in the embedded skill
// catalog, same constraint pipelineCrewCatalog documents).
func newCrewLeaseHarness(t *testing.T, skillIDs ...string) *crewLeaseHarness {
	t.Helper()
	revocations := newFakeRevocationStore()
	audits := &fakeAuditLogger{}
	agents := &AgentSkillServer{catalog: skills.GetDefault(), audit: audits}
	agents.SetRevocationStore(revocations)
	return &crewLeaseHarness{
		crew: &CrewServer{
			catalog: pipelineCrewCatalog(t, "test-crew", skillIDs...),
			skills:  skills.GetDefault(),
			agents:  agents,
			runs:    NewMemCrewRunStore(),
		},
		provisioner: &fakeCrewProvisioner{},
		revocations: revocations,
		wiper:       &fakeSeedWiper{},
		audits:      audits,
	}
}

// run drives the crew through RunCrew's own post-validation body with the
// harness's fakes substituted for the three collaborators a unit test cannot
// reach.
func (h *crewLeaseHarness) run(t *testing.T, runID string, send taskSender) (*pb.RunCrewResponse, error) {
	t.Helper()
	ctx := ctxAs("admin", true)
	crew, err := h.crew.catalog.Get("test-crew")
	if err != nil {
		t.Fatalf("catalog test-crew: %v", err)
	}
	return h.crew.runCrew(ctx, &pb.RunCrewRequest{
		CrewId:    "test-crew",
		RunId:     runID,
		GitSource: "https://github.com/org/repo",
		GitRef:    "main",
	}, crew, runID, crewRunDeps{
		provision: h.provisioner.provision,
		send:      send,
		wiper:     h.wiper,
	})
}

// assertLeaseEnded checks the full observable footprint of one member's lease
// having ended: both credentials revoked with `reason`, the seed files wiped
// and the seed dir + workspace removed from THAT member's box, and one
// agent.run_lease_end audit row naming the run.
func assertLeaseEnded(t *testing.T, h *crewLeaseHarness, runID, skillID, reason string) {
	t.Helper()

	for _, jti := range []string{"jti-platform-" + skillID, "jti-gateway-" + skillID} {
		revoked, err := h.revocations.IsRevoked(context.Background(), jti)
		if err != nil {
			t.Fatalf("IsRevoked(%s): %v", jti, err)
		}
		if !revoked {
			t.Errorf("member %s: credential %s was never revoked — its lease outlived the crew run", skillID, jti)
		}
