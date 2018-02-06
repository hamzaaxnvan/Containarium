package container

import (
	"testing"
	"time"
)

// TestRunBenchmarkBounded shrinks the per-phase time budget so the test stays
// fast, and asserts the benchmark produces positive scores within the bound.
func TestRunBenchmarkBounded(t *testing.T) {
	origCPU, origMem, origBuf := benchmarkCPUBudget, benchmarkMemBudget, benchmarkMemBufBytes
	benchmarkCPUBudget = 20 * time.Millisecond
	benchmarkMemBudget = 20 * time.Millisecond
	benchmarkMemBufBytes = 1 << 20
	defer func() {
		benchmarkCPUBudget, benchmarkMemBudget, benchmarkMemBufBytes = origCPU, origMem, origBuf
	}()

	start := time.Now()
	r := RunBenchmark()
	elapsed := time.Since(start)
