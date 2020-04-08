package proofpack

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/footprintai/containarium/internal/audit"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Sealing (#2798): the pack's digests are written as one row of the existing
// audit hash chain (internal/audit), through the same append path as every
// other audit row. No new hashing scheme: the part, header and pack digests
// are SHA-256 over deterministic proto encodings, exactly as PackDigest, and
// the row itself is hashed by the chain's own computeRowHash.

const (
	// SealAction is the audit_logs action of a seal row.
	SealAction = "proof_pack.seal"
	// SealResourceType is its resource_type; resource_id is the pack digest.
	SealResourceType = "proof_pack"
	// HeaderPart is TamperedPart.Step for the pack's non-step fields.
	HeaderPart = -1

	// AttachPath is where the sealed pack is committed in an agent-opened
	// PR's branch.
	AttachPath = ".containarium/proof-pack.json"

	// verifyBatch is the rows per VerifyChainSinceID call, as `audit verify`.
	verifyBatch = 1000
)

// ErrSealPurged is returned by Verify when retention has purged the seal's
// audit row: the pack can no longer be checked against the chain.
var ErrSealPurged = errors.New("proofpack: the seal's audit row was purged by retention")

// ErrSealNotFound is returned by Verify when the seal's audit row does not
// exist (and was not purged), or is not the seal of the pack.
var ErrSealNotFound = errors.New("proofpack: seal audit row not found")

// ErrChainBroken is returned by Verify when the audit chain from the seal
// row to the tip does not verify.
var ErrChainBroken = errors.New("proofpack: audit chain broken")

// TamperedPart names the part of a pack that does not match its seal.
type TamperedPart struct {
