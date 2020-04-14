package cmd

import (
	"strings"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestParseTrackerIssueListState(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    pb.TrackerIssueState
		wantErr bool
	}{
		{"empty matches any", "", pb.TrackerIssueState_TRACKER_ISSUE_STATE_UNSPECIFIED, false},
		{"open", "open", pb.TrackerIssueState_TRACKER_ISSUE_STATE_OPEN, false},
		{"closed", "closed", pb.TrackerIssueState_TRACKER_ISSUE_STATE_CLOSED, false},
		{"uppercase", "OPEN", pb.TrackerIssueState_TRACKER_ISSUE_STATE_OPEN, false},
		{"invalid", "merged", pb.TrackerIssueState_TRACKER_ISSUE_STATE_UNSPECIFIED, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseTrackerIssueListState(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (result %v)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("parseTrackerIssueListState(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseTrackerIssueNumber(t *testing.T) {
	if got, err := parseTrackerIssueNumber("42"); err != nil || got != 42 {
		t.Errorf("parseTrackerIssueNumber(42) = %d, %v, want 42, nil", got, err)
	}
	if _, err := parseTrackerIssueNumber("not-a-number"); err == nil {
		t.Error("parseTrackerIssueNumber(not-a-number) = nil error, want an error")
	}
}

func TestTrackerIssueStateLabel(t *testing.T) {
	tests := []struct {
		in   pb.TrackerIssueState
		want string
	}{
		{pb.TrackerIssueState_TRACKER_ISSUE_STATE_OPEN, "open"},
		{pb.TrackerIssueState_TRACKER_ISSUE_STATE_CLOSED, "closed"},
		{pb.TrackerIssueState_TRACKER_ISSUE_STATE_MERGED, "merged"},
		{pb.TrackerIssueState_TRACKER_ISSUE_STATE_UNSPECIFIED, "unspecified"},
	}
	for _, tt := range tests {
		if got := trackerIssueStateLabel(tt.in); got != tt.want {
			t.Errorf("trackerIssueStateLabel(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestTrackerCiVerdictLabel(t *testing.T) {
	tests := []struct {
		in   pb.TrackerCiVerdict
		want string
	}{
