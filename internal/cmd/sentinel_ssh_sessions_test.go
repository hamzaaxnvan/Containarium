//go:build !windows && !containarium_client

package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/sentinel/sshsession"
	"github.com/spf13/cobra"
)

// writeSessionJSONL is the test-local twin of the sshsession package's own
// writeJSONL helper — kept here rather than exported across packages since
// it's a one-line convenience, not shared behavior.
func writeSessionJSONL(t *testing.T, path string, recs ...sshsession.Record) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("open %q: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	for _, rec := range recs {
		line, err := json.Marshal(rec)
		if err != nil {
			t.Fatalf("marshal record: %v", err)
		}
		if _, err := f.Write(append(line, '\n')); err != nil {
			t.Fatalf("write record: %v", err)
		}
	}
}

func newSSHSessionsTestCmd(ctx context.Context, out *bytes.Buffer) *cobra.Command {
	cmd := &cobra.Command{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetContext(ctx)
	return cmd
}

// TestRunSentinelSSHSessionsList covers containarium#2004's `list` scope
// item: read the JSONL sink, print newest-first, filterable by
// session_id/login, with a --json passthrough. All fixture addresses are
// RFC 5737 test-net (never real), per this repo's CLAUDE.md.
func TestRunSentinelSSHSessionsList(t *testing.T) {
	open1 := sshsession.Record{
		SessionID: "sess-1", Phase: sshsession.SessionPhaseOpen,
		OccurredAt: time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC),
		ClientIP:   "203.0.113.10", Login: "alice", Target: "192.0.2.5:20022",
		AuthMethod: sshsession.AuthMethodPublicKey,
	}
	open2 := sshsession.Record{
		SessionID: "sess-2", Phase: sshsession.SessionPhaseOpen,
		OccurredAt: time.Date(2026, 1, 1, 10, 5, 0, 0, time.UTC),
		ClientIP:   "198.51.100.20", Login: "bob", Target: "192.0.2.6:20022",
		AuthMethod: sshsession.AuthMethodCertificate,
	}
	close1 := sshsession.Record{
		SessionID: "sess-1", Phase: sshsession.SessionPhaseClose,
		OccurredAt: time.Date(2026, 1, 1, 10, 10, 0, 0, time.UTC),
		ClientIP:   "203.0.113.10", Login: "alice", Target: "192.0.2.5:20022",
		CloseReason: sshsession.CloseReasonNormal,
	}

	tests := []struct {
		name       string
		sessionID  string
		login      string
		jsonOut    bool
		wantOrder  []string // session_id/phase, in printed order
		wantEmpty  bool
		wantErrSub string
	}{
		{
			name:      "no filter, newest first",
			wantOrder: []string{"sess-1/close", "sess-2/open", "sess-1/open"},
		},
		{
			name:      "filter by session_id",
			sessionID: "sess-1",
			wantOrder: []string{"sess-1/close", "sess-1/open"},
		},
		{
			name:      "filter by login",
			login:     "bob",
			wantOrder: []string{"sess-2/open"},
		},
		{
			name:      "filter matching nothing",
			sessionID: "does-not-exist",
			wantEmpty: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "ssh-sessions.jsonl")
			writeSessionJSONL(t, path, open1, open2, close1)

			t.Cleanup(resetSSHSessionsFlags)
			resetSSHSessionsFlags()
			sshSessionsFile = path
			sshSessionsSessionID = tt.sessionID
			sshSessionsLogin = tt.login
			sshSessionsJSON = tt.jsonOut

			var out bytes.Buffer
			cmd := newSSHSessionsTestCmd(context.Background(), &out)

			if err := runSentinelSSHSessionsList(cmd, nil); err != nil {
				t.Fatalf("runSentinelSSHSessionsList: %v", err)
			}

			if tt.wantEmpty {
				if !strings.Contains(out.String(), "No SSH session records found") {
					t.Fatalf("got %q, want the empty-result message", out.String())
				}
				return
			}

			gotOrder := extractSessionPhaseOrder(t, out.String())
			if len(gotOrder) != len(tt.wantOrder) {
				t.Fatalf("got order %v, want %v\nraw output:\n%s", gotOrder, tt.wantOrder, out.String())
			}
			for i := range gotOrder {
				if gotOrder[i] != tt.wantOrder[i] {
					t.Errorf("record %d = %q, want %q", i, gotOrder[i], tt.wantOrder[i])
				}
			}
		})
	}
}

// extractSessionPhaseOrder pulls "session_id/phase" out of either table or
// JSONL output, so the same test body can drive both without knowing which
// format it's reading in the JSON sub-cases.
func extractSessionPhaseOrder(t *testing.T, output string) []string {
	t.Helper()
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	var keys []string
