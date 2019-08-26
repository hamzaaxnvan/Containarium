package proofpack

import (
	"context"
	"errors"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/footprintai/containarium/internal/audit"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// fakeChain is an in-memory audit chain. Row hashes are not computed here:
// VerifyChainSinceID reports whatever bad is set to, and the real chain is
// exercised by seal_integration_test.go against Postgres.
type fakeChain struct {
	rows       map[int64]audit.ChainEntry
	next       int64
	base       audit.ChainBase
	purged     bool
	bad        int64
	verifyFrom []int64
}

func newFakeChain() *fakeChain { return &fakeChain{rows: map[int64]audit.ChainEntry{}} }

func (f *fakeChain) Append(_ context.Context, e *audit.AuditEntry) (int64, error) {
	f.next++
	row := audit.ChainEntry{AuditEntry: *e}
	row.ID = f.next
	f.rows[f.next] = row
	return f.next, nil
}

func (f *fakeChain) ChainEntryAt(_ context.Context, id int64) (audit.ChainEntry, bool, error) {
	r, ok := f.rows[id]
	return r, ok, nil
}

func (f *fakeChain) ChainBase(context.Context) (audit.ChainBase, bool, error) {
	return f.base, f.purged, nil
}

func (f *fakeChain) MaxRowID(context.Context) (int64, error) { return f.next, nil }

func (f *fakeChain) VerifyChainSinceID(_ context.Context, from int64, _ int) (int64, error) {
	f.verifyFrom = append(f.verifyFrom, from)
	return f.bad, nil
}

func samplePack() *pb.ProofPack {
	step := func(name string, kind pb.ProofPartKind, log string) *pb.ProofStep {
		return &pb.ProofStep{Name: name, Kind: kind, Log: []byte(log), LogSha256: LogDigest([]byte(log)), LogOriginalBytes: int64(len(log))}
	}
	return &pb.ProofPack{
		SchemaVersion: SchemaVersion,
		Producer:      "tenant-container",
		RepoDir:       DefaultRepoDir,
		GitCommit:     "0123456789abcdef0123456789abcdef01234567",
		CommandSource: pb.ProofCommandSource_PROOF_COMMAND_SOURCE_DECLARED,
		Steps: []*pb.ProofStep{
			step("unit", pb.ProofPartKind_PROOF_PART_KIND_UNIT, "ok  pkg 0.1s\n"),
			step("lint", pb.ProofPartKind_PROOF_PART_KIND_STATIC_ANALYSIS, "0 issues.\n"),
		},
		Status:        pb.ProofPackStatus_PROOF_PACK_STATUS_PASSED,
		LogLimitBytes: LogLimitBytes,
	}
}

func sealSample(t *testing.T, chain *fakeChain) *pb.SealedProofPack {
	t.Helper()
	logN := 3 // unrelated rows before the seal, so its id is not 1
	for i := 0; i < logN; i++ {
		_, _ = chain.Append(context.Background(), &audit.AuditEntry{Action: "container_create"})
	}
	sealed, err := Seal(context.Background(), chain, "tenant", "run-1", samplePack())
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	return sealed
}

func TestSeal_AppendsPackDigestToChain(t *testing.T) {
	chain := newFakeChain()
	sealed := sealSample(t, chain)

	digest, _ := PackDigest(samplePack())
	if sealed.GetSeal().GetPackSha256() != digest {
		t.Fatalf("seal pack_sha256 = %s, want PackDigest %s", sealed.GetSeal().GetPackSha256(), digest)
	}
	if got := sealed.GetSeal().GetAuditRowId(); got != 4 {
		t.Fatalf("audit_row_id = %d, want the appended row 4", got)
	}
	row := chain.rows[4]
	if row.Action != SealAction || row.ResourceType != SealResourceType || row.ResourceID != digest {
		t.Errorf("row = %s %s %s, want %s %s %s", row.Action, row.ResourceType, row.ResourceID, SealAction, SealResourceType, digest)
	}
	if row.Username != "tenant" || row.RunID != "run-1" {
		t.Errorf("row attribution = %q %q", row.Username, row.RunID)
	}
	if len(sealed.GetSeal().GetParts()) != 2 || sealed.GetSeal().GetParts()[1].GetName() != "lint" {
		t.Errorf("parts = %v, want one per step in order", sealed.GetSeal().GetParts())
	}
	if !proto.Equal(sealed.GetPack(), samplePack()) {
		t.Errorf("sealed pack differs from the input pack")
	}

	res, err := Verify(context.Background(), chain, sealed)
	if err != nil {
		t.Fatalf("Verify on an untouched sealed pack: %v", err)
	}
	if res.AuditRowID != 4 || res.PackSHA256 != digest || res.Steps != 2 {
		t.Errorf("result = %+v", res)
	}
	if len(chain.verifyFrom) == 0 || chain.verifyFrom[0] != 3 {
		t.Errorf("chain walk started at %v, want from the row before the seal (3)", chain.verifyFrom)
	}
}

// Editing any part of a sealed pack makes verify fail and name the part.
func TestVerify_EditedPartIsNamed(t *testing.T) {
	cases := []struct {
		name     string
		edit     func(p *pb.ProofPack)
		wantStep int
		wantName string
	}{
		{"one log byte", func(p *pb.ProofPack) { p.Steps[1].Log[0] ^= 1 }, 1, "lint"},
