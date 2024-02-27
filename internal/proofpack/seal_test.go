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
