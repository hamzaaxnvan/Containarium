package sshsession

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// Recorder appends one session lifecycle Record to a durable sink.
type Recorder interface {
	Record(rec Record) error
}

// JSONLRecorder appends one JSON object per line — the stable,
// machine-readable sink containerium#1980 requires: every record has the
// same field names (see Record), so a consumer never needs a regex to
// parse one. Safe for concurrent use.
type JSONLRecorder struct {
	mu   sync.Mutex
	w    io.Writer
	file *os.File // non-nil only when this recorder opened its own file.
	path string   // the path file was opened at; empty for a writer-backed recorder.
}

// NewJSONLFileRecorder opens (creating if needed) path for append-only
// writes and returns a Recorder backed by it. The file is opened 0600:
// these records name a real client IP and a login/target pairing —
// operational metadata, not secret material (see Credential's doc
// comment), but still not meant to be world-readable.
func NewJSONLFileRecorder(path string) (*JSONLRecorder, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("create session record sink directory %q: %w", dir, err)
		}
	}

	// #nosec G304 -- path is the operator's own --records-file flag on the
	// sentinel's ssh-session-plugin subcommand (see
	// internal/cmd/sentinel_ssh_session_plugin.go), not attacker-controlled
	// input. Choosing where the sink lands is the flag's entire purpose,
	// the same shape as internal/cmd/sentinel_pprof.go's --output path.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open session record sink %q: %w", path, err)
	}

	return &JSONLRecorder{w: f, file: f, path: path}, nil
}

// NewJSONLRecorder wraps an already-open writer (e.g. a bytes.Buffer in
