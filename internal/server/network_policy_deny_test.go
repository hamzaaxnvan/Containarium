package server

import (
	"fmt"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/netbpf"
	"github.com/footprintai/containarium/internal/netpolicy"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// fakeDenyApplier records AddDeny/DeleteDeny calls and can be told to fail an
// AddDeny for a given key, so applyDeny's apply/retry semantics are testable
// without a kernel.
type fakeDenyApplier struct {
	added   []netbpf.DenyEntry
	deleted []netbpf.DenyKey
	failAdd map[netbpf.DenyKey]bool
}

func (f *fakeDenyApplier) AddDeny(e netbpf.DenyEntry) error {
	if f.failAdd[e.Key()] {
		return fmt.Errorf("inject add failure")
	}
	f.added = append(f.added, e)
	return nil
}

func (f *fakeDenyApplier) DeleteDeny(k netbpf.DenyKey) error {
	f.deleted = append(f.deleted, k)
	return nil
}

func TestPlanReconcile_DenyRules(t *testing.T) {
	policies := map[string]netpolicy.CompiledPolicy{
