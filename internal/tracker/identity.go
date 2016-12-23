package tracker

import (
	"fmt"
	"regexp"
	"strings"
)

// Identity is the platform-verified attribution for one write. Built
// only from a verified JWT's claims and the run record they resolve
// to — never from request fields, so a caller cannot forge or
// influence it. See Stamp.
type Identity struct {
	RunID   string
	SkillID string
	// Model is the skill manifest's declared model tier; empty when the
	// manifest declares none, in which case Stamp omits the parenthesized
	// model segment entirely rather than printing "()".
	Model string
}

// StampKind labels what kind of write a marker is attached to.
// ClaimTrackerIssue's claim-detection scan looks specifically for
// KindClaim.
type StampKind string

const (
	KindClaim   StampKind = "claim"
	KindComment StampKind = "comment"
	KindChange  StampKind = "change"
	// KindYield marks a comment as its own run voiding a claim it just
	// posted (ClaimTrackerIssue's post-claim re-read step) — the run's
