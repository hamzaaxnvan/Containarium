package tracker

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// TestPolicy_Defaults pins the documented defaults (design:
// docs/architecture/issue-triggered-agents.md, "Chain guards"): a nil or
// zero-valued TrackerPolicy yields the same effective policy, and an
// explicitly set field is kept while the rest still default.
func TestPolicy_Defaults(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   *pb.TrackerPolicy
	}{
		{"nil", nil},
		{"zero value", &pb.TrackerPolicy{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := PolicyFromProto(tc.in)
			if !reflect.DeepEqual(p.LabelAllowList, DefaultLabelAllowList) {
				t.Errorf("LabelAllowList = %v, want defaults %v", p.LabelAllowList, DefaultLabelAllowList)
			}
			if p.AutoChain {
				t.Error("AutoChain = true, want false (human gate by default)")
			}
			if p.MaxDepth != DefaultMaxDepth {
				t.Errorf("MaxDepth = %d, want %d", p.MaxDepth, DefaultMaxDepth)
			}
			if p.MaxChildrenPerRun != DefaultMaxChildrenPerRun {
				t.Errorf("MaxChildrenPerRun = %d, want %d", p.MaxChildrenPerRun, DefaultMaxChildrenPerRun)
			}
			if p.RunTimeout != DefaultRunTimeout {
				t.Errorf("RunTimeout = %v, want %v", p.RunTimeout, DefaultRunTimeout)
			}
		})
	}

	t.Run("explicit fields kept, rest default", func(t *testing.T) {
		p := PolicyFromProto(&pb.TrackerPolicy{MaxDepth: 1, AutoChain: true, RunTimeoutSeconds: 90})
		if p.MaxDepth != 1 || !p.AutoChain || p.RunTimeout != 90*time.Second {
			t.Errorf("explicit fields not kept: %+v", p)
		}
		if p.MaxChildrenPerRun != DefaultMaxChildrenPerRun {
			t.Errorf("MaxChildrenPerRun = %d, want default %d", p.MaxChildrenPerRun, DefaultMaxChildrenPerRun)
		}
		if !reflect.DeepEqual(p.LabelAllowList, DefaultLabelAllowList) {
			t.Errorf("LabelAllowList = %v, want defaults", p.LabelAllowList)
		}
	})
}

// TestPolicy_LabelAllowList is the design's named table: exact, glob
// scope:*, rejected state label agent:done, rejected arbitrary
// deploy:prod, empty list = defaults — plus the reserved-state-label
// rule that no allow-list can lift for a run token.
func TestPolicy_LabelAllowList(t *testing.T) {
	tests := []struct {
		name     string
		allow    []string
		label    string
		runToken bool
		wantErr  error // nil = allowed
	}{
		{"exact gate label", nil, "agent:needs-approval", true, nil},
		{"glob scope:*", nil, "scope:architecture", true, nil},
		{"glob model:*", nil, "model:fable", true, nil},
		{"state label agent:done rejected for a run", nil, "agent:done", true, ErrStateLabelReserved},
		{"state label agent:queued rejected for a run", nil, "agent:queued", true, ErrStateLabelReserved},
		{"arbitrary deploy:prod rejected", nil, "deploy:prod", true, ErrLabelNotAllowed},
		{"empty list means defaults", []string{}, "scope:product", true, nil},
		{"custom list replaces defaults", []string{"triaged"}, "scope:product", true, ErrLabelNotAllowed},
		{"custom list allows its own entry", []string{"triaged"}, "triaged", true, nil},
		{"agent:* cannot lift the reserved rule for a run", []string{"agent:*"}, "agent:done", true, ErrStateLabelReserved},
		{"agent:* still allows the gate label for a run", []string{"agent:*"}, "agent:needs-approval", true, nil},
		{"operator with agent:* may write a state label", []string{"agent:*"}, "agent:done", false, nil},
		{"operator without agent:* may not", nil, "agent:done", false, ErrLabelNotAllowed},
		{"case-sensitive", nil, "Scope:product", true, ErrLabelNotAllowed},
		{"star spans slashes", []string{"area/*"}, "area/api/v2", true, nil},
		{"prefix without star is exact", []string{"scope:"}, "scope:product", true, ErrLabelNotAllowed},
		{"empty label rejected", nil, "", true, ErrLabelInvalid},

		// Review of #2034, blocking: GitLab's adapter comma-joins labels and
		// GitLab splits them back, so one "label" must never smuggle a second.
		{"comma smuggles a second label", nil, "scope:x,agent:done", true, ErrLabelInvalid},
		{"comma smuggles deploy:prod", nil, "scope:x,deploy:prod", true, ErrLabelInvalid},
		{"newline smuggles a second label", nil, "scope:x\nagent:done", true, ErrLabelInvalid},
		{"control character rejected", nil, "scope:x\tagent", true, ErrLabelInvalid},
		{"leading whitespace rejected", nil, " scope:x", true, ErrLabelInvalid},
		{"trailing whitespace rejected", nil, "agent:done ", true, ErrLabelInvalid},
		{"comma rejected even for an operator with *", []string{"*"}, "a,b", false, ErrLabelInvalid},
		{"inner space is a legal label", []string{"good first issue"}, "good first issue", true, nil},
