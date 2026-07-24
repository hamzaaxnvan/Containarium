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
