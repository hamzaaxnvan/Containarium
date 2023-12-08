package server

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/proofpack"
	"github.com/footprintai/containarium/internal/runlease"
	"github.com/footprintai/containarium/pkg/core/crews"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// #2799: the engineer-crew's Proof Pack comes from a box other than the
// coder's. These tests drive runCrew with the real embedded engineer-crew and
// fakes for the collaborators a unit test cannot reach (box provisioning, A2A,
// in-box exec).

const (
	packImplArtifact  = `{"files":[{"path":"widget.go","content":"package widget\n"}],"summary":"add widget","pr_title":"Add widget","pr_body":"b","tests_run":true}`
	packFinalArtifact = `{"files":[{"path":"widget.go","content":"package widget\n\n// reviewed\n"}],"drafter_summary":"add widget","review_notes":"comment","pr_title":"Add widget","pr_body":"b","tests_run":true}`
)

// packCall is one producePack invocation.
type packCall struct{ box, dir, artifact string }

type packHarness struct {
	*crewLeaseHarness
	producerProvisioned []string
	producerBox         string // box the fake provisionProducer hands back
	packs               []packCall
	pack                *pb.ProofPack
	packErr             error
}

func newPackHarness(t *testing.T) *packHarness {
	t.Helper()
	h := newCrewLeaseHarness(t, "relay-agent", "hello-agent")
	h.crew.catalog = crews.GetDefault()
	return &packHarness{crewLeaseHarness: h, producerBox: "agent-test-runner-container"}
}

func (h *packHarness) provisionProducer(_ context.Context, skill *pb.AgentSkill, _ *pb.RunCrewRequest, runID string) (runlease.Lease, string, error) {
	h.producerProvisioned = append(h.producerProvisioned, skill.Id)
	return runlease.Lease{RunID: runID, Box: h.producerBox, SeedDir: seedDirFor(runID), Workspace: workspaceDirFor(runID)}, "commit-base", nil
}

func (h *packHarness) producePack(_ context.Context, box, dir, artifact string) (*pb.ProofPack, error) {
	h.packs = append(h.packs, packCall{box, dir, artifact})
	if h.packErr != nil {
		return nil, h.packErr
	}
	p := h.pack
	if p == nil {
		p = &pb.ProofPack{SchemaVersion: 1, Producer: box, GitCommit: "commit-base", Status: pb.ProofPackStatus_PROOF_PACK_STATUS_PASSED}
	}
	return p, nil
}

func (h *packHarness) runEngineerCrew(t *testing.T, runID string, send taskSender) *pb.CrewRun {
	t.Helper()
	crew, err := h.crew.catalog.Get("engineer-crew")
