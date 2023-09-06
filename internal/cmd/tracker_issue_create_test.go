package cmd

import (
	"testing"
	"time"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Table-driven flag validation for `tracker issue create` — the request
// builder is the seam the cobra RunE hands its flags to, so it's what
// gets tested (same convention as parseTrackerProvider / printTrackerStatus).
func TestBuildCreateTrackerIssueRequest(t *testing.T) {
	tests := []struct {
		name    string
		title   string
		body    string
		labels  []string
		parent  int64
		wantErr bool
	}{
		{"happy path", "Design the thing", "body", []string{"scope:architecture"}, 42, false},
		{"no parent is fine for the builder (daemon decides per token)", "t", "", nil, 0, false},
		{"labels are trimmed", "t", "", []string{"  scope:architecture "}, 1, false},
		{"empty title", "", "body", nil, 1, true},
