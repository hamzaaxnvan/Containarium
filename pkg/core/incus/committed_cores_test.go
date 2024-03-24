package incus

import (
	"math"
	"testing"
)

// TestCommittedCores pins the request-string → committed-cores mapping the
// #1029 capacity accounting sums over. It must agree with what CreateContainer
// actually wrote (post-#1034 hard quotas), read legacy percentage allowances,
// count CPU-set cardinality, and never blow up on junk (returns 0, since a
// single bad entry must not make a whole host un-schedulable).
func TestCommittedCores(t *testing.T) {
	cases := []struct {
		in   string
		want float64
	}{
