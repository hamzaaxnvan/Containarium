package sshsession

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// DefaultRecordsFile is the sink path used when nothing more specific is
// configured — the same default `containarium sentinel ssh-session-plugin`
// (see internal/cmd/sentinel_ssh_session_plugin.go) and `containarium
// sentinel ssh-sessions list`/`follow` (containarium#2004) fall back to, so
// there is exactly one place a change to that default has to happen.
const DefaultRecordsFile = "/var/log/containarium/ssh-sessions.jsonl"

// Filter narrows which records ReadRecords/Follow return. A zero Filter
// matches every record — an empty field is "don't filter on this", not
// "match empty".
type Filter struct {
	SessionID string
	Login     string
}

func (f Filter) matches(rec Record) bool {
	if f.SessionID != "" && rec.SessionID != f.SessionID {
		return false
	}
	if f.Login != "" && rec.Login != f.Login {
		return false
	}
	return true
}

// ReadRecords decodes every JSON-line record from r matching filter, in the
// order they appear in the stream — the order JSONLRecorder.Record appends
// them in, i.e. oldest first. Callers wanting newest-first (the `list`
// subcommand's default per containarium#2004) reverse the result
// themselves; Follow's own "as it happens" order is naturally
// chronological, so this function never reorders.
//
// A blank line is skipped rather than treated as an error — harmless
// against a sink whose last line ends in a trailing newline. A line that
// IS non-blank but fails to parse as a Record is an error: it means the
// sink is not what this reader expects, which is worth surfacing rather
// than silently dropping.
func ReadRecords(r io.Reader, filter Filter) ([]Record, error) {
	var out []Record
	scanner := bufio.NewScanner(r)
	// A session record is small, but don't let bufio.Scanner's 64KiB
	// default cap silently truncate a pathological line.
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		var rec Record
		if err := json.Unmarshal([]byte(text), &rec); err != nil {
			return out, fmt.Errorf("parse record at line %d: %w", lineNum, err)
		}
		if filter.matches(rec) {
			out = append(out, rec)
		}
	}
	if err := scanner.Err(); err != nil {
		return out, fmt.Errorf("read records: %w", err)
