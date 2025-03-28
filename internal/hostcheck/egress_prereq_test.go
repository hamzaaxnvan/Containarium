package hostcheck

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// The coding-CLI egress program (#2370, design PR 3 = #2379) needs kernel >=
// 6.6, a unified cgroup v2 hierarchy and the cgroup_skb program type. Each
// missing prerequisite must fail with its own named reason, and anything the
// probe could not determine is a failure, never a pass.
func TestEgressPrereqChecks(t *testing.T) {
	supported := egressPrereqProbes{
		kernelRelease: func() (string, error) { return "6.8.0-generic", nil },
		cgroupV2:      func() (bool, error) { return true, nil },
		cgroupSKB:     func() error { return nil },
	}
	cases := map[string]struct {
		mutate     func(*egressPrereqProbes)
