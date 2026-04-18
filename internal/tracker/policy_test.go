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
