package tracker

import (
	"context"
	"errors"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// TestRecordChild_DepthFloorsAtTheRunsOwnDispatchDepth pins the #2073 fix
// at the store: a child's depth is max(parent's recorded depth, the
// calling run's own dispatch depth) + 1. The run's dispatch depth is what
// the dispatcher wrote on its tracker_dispatches row from the lineage
// table when it started the run — never anything the caller supplies —
// so a run cannot lower its effective depth by naming a shallower
// parent_number. A run with no dispatch row (started by a human, not the
// dispatcher) has no floor above the parent's depth, as before.
