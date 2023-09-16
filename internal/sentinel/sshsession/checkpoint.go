package sshsession

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// DefaultCheckpointFile is where the shipper persists its progress.
const DefaultCheckpointFile = "/var/lib/containarium/ssh-session-shipper/checkpoint.json"

// DefaultStandaloneCheckpointFile is the standalone `ssh-sessions ship`
// command's default. It must differ from DefaultCheckpointFile: the
// in-process shipper routes per backend, the standalone command sends
// everything to one, and sharing a file would let one move Base past records
// the other's backends never confirmed.
const DefaultStandaloneCheckpointFile = "/var/lib/containarium/ssh-session-shipper/standalone-checkpoint.json"

// Checkpoint is the shipper's durable progress marker over the JSONL sink.
//
// Progress is tracked per backend, because every record is shipped to the
// backend its login routes to, and one unreachable backend must not stall the
// others:
//
//   - Inode identifies the file the offsets refer to, so a rename-style
//     rotation is detected (a path alone cannot tell the rotated file from
//     its replacement).
//   - Base is a byte offset before which EVERY backend is fully caught up.
//     Each pass starts scanning at Base.
//   - Offsets holds, for a backend that is ahead of Base, the offset after
//     the last record confirmed shipped to it. A backend with no entry is
//     exactly at Base.
//
// An offset only ever moves after the backend answered OK for the batch it
// covers — the checkpoint rule that, together with the server's dedupe,
// gives at-least-once delivery with exactly-once rows.
type Checkpoint struct {
	Inode uint64 `json:"inode"`
	// Head is a hash of the file's first complete line. An append-only file's
	// first line never changes, so a different one means the path now holds a
	// different file even if the inode number is the same (inode numbers are
	// reused after a delete). Empty until the first line is complete.
	Head    string           `json:"head,omitempty"`
	Base    int64            `json:"base"`
