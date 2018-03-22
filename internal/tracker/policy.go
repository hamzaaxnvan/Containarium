package tracker

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Label contract for issue-triggered role agents (#2024; PRD
// docs/product/issue-triggered-agents.md, "Label contract"; design
// docs/architecture/issue-triggered-agents.md, "Chain guards").
const (
	// LabelNeedsApproval gates an agent-filed follow-up until a human
	// removes it. Forced onto every follow-up a run-scoped token files
	// unless the connection's policy opted into auto_chain.
	LabelNeedsApproval = "agent:needs-approval"

	// The dispatcher's state labels. Written ONLY by the dispatcher's own
	// code path (#2022) — never by a run, whatever its allow-list says.
	LabelAgentQueued  = "agent:queued"
	LabelAgentRunning = "agent:running"
	LabelAgentDone    = "agent:done"
	LabelAgentFailed  = "agent:failed"
)

// ReservedStateLabels is the set a run-scoped token can never write,
// through CreateTrackerIssue or SetTrackerIssueLabels: an injected
// "mark this done" must not be able to move a dispatch's state.
var ReservedStateLabels = []string{LabelAgentQueued, LabelAgentRunning, LabelAgentDone, LabelAgentFailed}

// DefaultLabelAllowList is the design's default: role scopes, model
// routing, and the approval gate. Deliberately NOT agent:* — the state
// labels above must stay out of a run's reach.
var DefaultLabelAllowList = []string{"scope:*", "model:*", LabelNeedsApproval}

// Documented defaults for the zero values of TrackerPolicy.
const (
	DefaultMaxDepth          int32 = 3
	DefaultMaxChildrenPerRun int32 = 5
	DefaultRunTimeout              = time.Hour
)

var (
	// ErrLabelNotAllowed: a label outside the connection's allow-list.
	ErrLabelNotAllowed = errors.New("tracker: label is not in the connection's allow-list")
	// ErrStateLabelReserved: a run-scoped token tried to write one of
	// ReservedStateLabels. No allow-list entry can lift this.
	ErrStateLabelReserved = errors.New("tracker: agent state labels are written only by the dispatcher")
	// ErrLabelInvalid: a label that is not safe to put on the wire — empty,
	// padded with whitespace, or containing ',' or a control character.
	// GitLab takes labels as ONE comma-joined string and splits it back, so
	// "scope:x,agent:done" would otherwise pass the allow-list as one label
	// and land as two (review of #2034).
