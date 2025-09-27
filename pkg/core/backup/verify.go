package backup

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// VerificationResult is the outcome of a restore test. Kept as a string
// in the core so the package stays free of the pb dependency; the server
// maps it to/from pb.VerificationResult.
type VerificationResult string

const (
	VerificationPassed VerificationResult = "passed"
	VerificationFailed VerificationResult = "failed"
)

// Check is one engine-appropriate assertion made during a restore test,
// recorded individually so the evidence shows *what* was checked rather
// than just a pass/fail bit.
type Check struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail"`
}

// Verification is the audit artifact for one restore test: who ran it,
// when, against what, and what the engine said. Persisted on the Record
// so the evidence outlives the run that produced it.
type Verification struct {
	VerifiedAt      time.Time          `json:"verified_at"`
	Result          VerificationResult `json:"result"`
	Error           string             `json:"error,omitempty"`
	TargetContainer string             `json:"target_container"`
	ScratchDatabase string             `json:"scratch_database"`
	DurationMS      int64              `json:"duration_ms"`
	VerifiedBy      string             `json:"verified_by,omitempty"`
	Checks          []Check            `json:"checks,omitempty"`
}

// VerifyOptions parameterizes a restore test.
type VerifyOptions struct {
	// ID of the backup to verify.
	ID string
	// TargetContainer is the throwaway container the dump is restored
	// into. Required, and must differ from SourceContainer.
	TargetContainer string
	// SourceContainer is the container the backup was taken from,
	// resolved by the caller. Verification refuses to run when it
	// matches TargetContainer — a restore test must never be able to
	// load a dump over the database it came from.
	SourceContainer string
	// Conn is the target container's Postgres connection. Conn.Database
	// is ignored: the scratch database name is generated here.
	Conn PgConn
	// VerifiedBy is the authenticated subject requesting the test, for
	// the "who" half of the audit record.
	VerifiedBy string
	// AgeIdentity ("AGE-SECRET-KEY-1…") decrypts an Encrypted record for
	// this one call, same contract as RestoreOptions.AgeIdentity (#1831).
	// Required for an encrypted record — the platform holds no
	// decryption key of its own, so verification refuses to even attempt
	// a restore test without it. Given the wrong identity, decryption
	// fails and is recorded as a FAILED verification (a result, not an
	// error) so the failure survives as audit evidence (#2295).
	AgeIdentity string
}

// scratchPrefix marks the throwaway databases verification creates, so a
// leaked one is identifiable on sight during an incident.
const scratchPrefix = "containarium_verify_"

// maxIdentLen is Postgres's identifier limit; a longer name is silently
// truncated by the server, which would break the DROP that pairs with the
// CREATE. Truncate deliberately instead.
const maxIdentLen = 63

// Verify restore-tests a stored dump against a throwaway database inside
// TargetContainer and records the outcome on the backup record.
//
// An unrestorable dump is a *result*, not an error: it comes back as a
// Verification with Result == VerificationFailed and the engine's own
// message in Error. A non-nil error means the test could not be run at
// all (unknown backup, missing or unsafe target) — that distinction is
// what lets a scheduled verification report "backup X is not restorable"
// instead of failing like a broken job.
func (m *Manager) Verify(opts VerifyOptions) (*Verification, error) {
	r, err := m.Get(opts.ID)
	if err != nil {
		return nil, err
	}
	if opts.TargetContainer == "" {
		return nil, fmt.Errorf("target container is required: a restore test needs a throwaway container to load into")
	}
	// A restore test only means something for a dump the platform can
	// load. An undeclared hook dump is opaque (#1831) — refuse it here,
	// before a scratch database exists, rather than recording a FAILED
	// verification that reads like a corrupt backup. A hook that declared
	// a pg_custom archive (#2405) is the same bytes database mode
	// produces, so it runs every check below unchanged.
	if r.Engine == EngineBox {
		return nil, errBoxRecordNotDatabase(r.ID, "restore-tested")
	}
	if r.Engine == EngineHook && r.HookFormat != HookFormatPGCustom {
		return nil, fmt.Errorf("backup %s was produced by tenant hook %s and is an opaque stream: the platform cannot restore-test it", r.ID, r.Hook)
	}
	// An encrypted dump needs the matching identity to even attempt a
