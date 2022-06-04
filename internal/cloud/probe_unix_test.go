//go:build !windows

package cloud

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestDiskGB_ReportsPositiveTotal(t *testing.T) {
	// "/" (the fallback) always exists on a unix test host, so total disk
	// should be a positive GB figure and available should not exceed it.
	total, avail := diskGB()
	if total <= 0 {
		t.Errorf("total disk GB should be positive, got %d", total)
	}
	if avail < 0 || avail > total {
		t.Errorf("available disk GB %d out of range (total %d)", avail, total)
	}
}
