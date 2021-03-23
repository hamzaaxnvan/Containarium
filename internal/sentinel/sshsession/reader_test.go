package sshsession

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeJSONL writes one JSON-encoded record per line to path, creating it
// if needed. Test helper only.
func writeJSONL(t *testing.T, path string, recs ...Record) {
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

func TestReadRecords_FiltersAndParsesInFileOrder(t *testing.T) {
	rec1 := Record{
		SessionID: "sess-1", Phase: SessionPhaseOpen,
		OccurredAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		ClientIP:   "203.0.113.10", Login: "alice",
	}
	rec2 := Record{
		SessionID: "sess-2", Phase: SessionPhaseOpen,
		OccurredAt: time.Date(2026, 1, 1, 0, 1, 0, 0, time.UTC),
		ClientIP:   "198.51.100.20", Login: "bob",
	}
	rec3 := Record{
		SessionID: "sess-1", Phase: SessionPhaseClose,
		OccurredAt: time.Date(2026, 1, 1, 0, 2, 0, 0, time.UTC),
		ClientIP:   "203.0.113.10", Login: "alice",
		CloseReason: CloseReasonNormal,
	}

	tests := []struct {
		name   string
		filter Filter
		want   []string // expected SessionID+Phase pairs, in order
	}{
		{
			name:   "no filter returns everything in file order",
			filter: Filter{},
			want:   []string{"sess-1/open", "sess-2/open", "sess-1/close"},
		},
		{
			name:   "filter by session_id",
			filter: Filter{SessionID: "sess-1"},
			want:   []string{"sess-1/open", "sess-1/close"},
		},
		{
			name:   "filter by login",
			filter: Filter{Login: "bob"},
			want:   []string{"sess-2/open"},
		},
		{
			name:   "filter by both session_id and login, no match",
			filter: Filter{SessionID: "sess-1", Login: "bob"},
			want:   nil,
		},
		{
			name:   "filter matching nothing",
			filter: Filter{SessionID: "does-not-exist"},
			want:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			for _, rec := range []Record{rec1, rec2, rec3} {
				line, err := json.Marshal(rec)
				if err != nil {
					t.Fatalf("marshal: %v", err)
				}
				buf.Write(line)
				buf.WriteByte('\n')
			}

			got, err := ReadRecords(&buf, tt.filter)
			if err != nil {
				t.Fatalf("ReadRecords: %v", err)
			}

			gotKeys := make([]string, len(got))
			for i, r := range got {
				gotKeys[i] = r.SessionID + "/" + string(r.Phase)
			}
			if len(gotKeys) != len(tt.want) {
				t.Fatalf("got %v, want %v", gotKeys, tt.want)
			}
			for i := range gotKeys {
				if gotKeys[i] != tt.want[i] {
					t.Errorf("record %d = %q, want %q", i, gotKeys[i], tt.want[i])
				}
			}
		})
	}
}

func TestReadRecords_SkipsBlankLines(t *testing.T) {
	buf := bytes.NewBufferString("\n\n")
	line, _ := json.Marshal(Record{SessionID: "sess-1", Phase: SessionPhaseOpen})
	buf.Write(line)
	buf.WriteString("\n\n")

	got, err := ReadRecords(buf, Filter{})
	if err != nil {
		t.Fatalf("ReadRecords: %v", err)
	}
	if len(got) != 1 || got[0].SessionID != "sess-1" {
		t.Fatalf("got %+v, want one record for sess-1", got)
	}
}

func TestReadRecords_InvalidJSONLineIsAnError(t *testing.T) {
	buf := bytes.NewBufferString("not-json\n")
	if _, err := ReadRecords(buf, Filter{}); err == nil {
		t.Fatal("expected an error for a malformed line, got nil")
	}
}

func TestReadRecordsFile_MissingFileReturnsEmptyNotError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "does-not-exist.jsonl")

	got, err := ReadRecordsFile(path, Filter{})
	if err != nil {
		t.Fatalf("ReadRecordsFile: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d records, want 0", len(got))
	}
}

func TestReadRecordsFile_ReadsWhatWasWritten(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ssh-sessions.jsonl")

	writeJSONL(t, path,
		Record{SessionID: "sess-1", Phase: SessionPhaseOpen, Login: "alice", ClientIP: "203.0.113.10"},
		Record{SessionID: "sess-1", Phase: SessionPhaseClose, Login: "alice", ClientIP: "203.0.113.10"},
	)

	got, err := ReadRecordsFile(path, Filter{})
	if err != nil {
		t.Fatalf("ReadRecordsFile: %v", err)
	}
	if len(got) != 2 {
