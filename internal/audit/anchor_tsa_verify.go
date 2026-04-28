package audit

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
)

// #2728: check the local anchor file and the database against the TSA
// tokens stored next to the file.
//
// The tokens live on the same host as the file, so a host-root attacker can
// delete them; what they cannot do is make a token vouch for an edited
// record, because the token is signed by the TSA over the record's hash.
// Hence: a token that does not verify, or covers a different record than the
// local line, is a failure; a local checkpoint with no token is only a
// warning (Q6).

// TSAFindingKind classifies one result of VerifyAnchorsAgainstTSA.
type TSAFindingKind int

const (
	TSAFindingUnspecified TSAFindingKind = iota

	// Failures.

	// TSAFindingTokenInvalid: a token is malformed, its signature does not
	// verify, or it carries no TSA certificate.
	TSAFindingTokenInvalid
	// TSAFindingUntrustedSigner: a token is not signed by a TSA chaining to
	// the trust bundle at the token's genTime.
	TSAFindingUntrustedSigner
	// TSAFindingImprintMismatch: a valid token covers a different record than
	// the local anchor file holds for its checkpoint: the line was edited.
	TSAFindingImprintMismatch
	// TSAFindingMissingLocally: a token exists for a checkpoint the local file
	// lacks. The daemon writes the file first, so a line was removed.
	TSAFindingMissingLocally
	// TSAFindingDatabaseRootDiffers: the database row at an anchored
	// checkpoint no longer hashes to the anchored root, or no longer exists.
	TSAFindingDatabaseRootDiffers

	// Warnings.

	// TSAFindingNoToken: local checkpoints have no token (anchored before the
	// TSA was configured, or a stamp that has not succeeded yet).
	TSAFindingNoToken
	// TSAFindingUnrecognisedFile: a file in the token directory is not named
	// like a token. Never counted as one.
	TSAFindingUnrecognisedFile
	// TSAFindingBelowChainBase: anchored checkpoints lie below the chain base
	// (rows purged by retention, #2878); their tokens are checked against the
	// local file, not the database.
	TSAFindingBelowChainBase
)

func (k TSAFindingKind) String() string {
	switch k {
	case TSAFindingTokenInvalid:
		return "token-invalid"
	case TSAFindingUntrustedSigner:
		return "untrusted-signer"
	case TSAFindingImprintMismatch:
		return "imprint-mismatch"
	case TSAFindingMissingLocally:
		return "missing-locally"
	case TSAFindingDatabaseRootDiffers:
		return "database-root-differs"
	case TSAFindingNoToken:
		return "no-token"
	case TSAFindingUnrecognisedFile:
		return "unrecognised-file"
	case TSAFindingBelowChainBase:
		return "below-chain-base"
	}
	return "unspecified"
}

// TSAAnchorFinding is one failure or warning. Checkpoint is 0 when the
