//go:build !windows && !containarium_client

package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/audit"
)

func TestVerifyStartID(t *testing.T) {
	base := audit.ChainBase{ID: 510, RowsPurged: 509, PurgedAt: time.Date(2026, 10, 23, 11, 0, 0, 0, time.UTC)}
	tests := []struct {
		name     string
		from     int64
		purged   bool
		want     int64
		wantNote string
	}{
		{"never purged starts where asked", 0, false, 0, ""},
		{"never purged keeps a from-id", 700, false, 700, ""},
		{"from genesis starts at the base", 0, true, 510, "verifying from the stored chain base"},
