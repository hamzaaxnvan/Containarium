package server

import (
	"fmt"
	"sort"
	"testing"
)

// fakeIPTenantApplier records SetIPTenant/DeleteIPTenant calls and can be told
// to fail a Set for a given IP, so applyIPTenant's apply/retry semantics are
// testable without a kernel.
type fakeIPTenantApplier struct {
	set     map[[4]byte]uint32
	deleted [][4]byte
	failSet map[[4]byte]bool
}

func newFakeIPTenantApplier() *fakeIPTenantApplier {
	return &fakeIPTenantApplier{set: map[[4]byte]uint32{}, failSet: map[[4]byte]bool{}}
}

func (f *fakeIPTenantApplier) SetIPTenant(ip [4]byte, tid uint32) error {
	if f.failSet[ip] {
		return fmt.Errorf("inject set failure")
	}
	f.set[ip] = tid
	return nil
}

func (f *fakeIPTenantApplier) DeleteIPTenant(ip [4]byte) error {
	f.deleted = append(f.deleted, ip)
	return nil
}

func TestDiffIPTenant(t *testing.T) {
	a := [4]byte{10, 0, 3, 1}
	b := [4]byte{10, 0, 3, 2}
	c := [4]byte{10, 0, 3, 3}

	installed := map[[4]byte]uint32{
		a: 1, // unchanged
		b: 1, // tenant changes to 2 -> re-set
		c: 1, // no longer desired -> delete
	}
	desired := map[[4]byte]uint32{
		a: 1,
		b: 2,
	}

	set, del := diffIPTenant(installed, desired)

	if got, ok := set[b]; !ok || got != 2 {
		t.Errorf("b should be re-set to tenant 2, got set=%v", set)
	}
	if _, ok := set[a]; ok {
		t.Errorf("a is unchanged and must not be re-set, got set=%v", set)
	}
	if len(del) != 1 || del[0] != c {
		t.Errorf("only c should be deleted, got del=%v", del)
	}
}

