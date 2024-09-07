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
