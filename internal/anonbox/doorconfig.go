package anonbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// DefaultDoorStatePath is where the daemon persists the door's operator
// state so a kill switch or a ban survives a restart (#2200).
const DefaultDoorStatePath = "/var/lib/containarium/anon-door.json"

// DoorConfig is the operator-settable state of the door: open or closed
// (with the message a refused caller is told), and the banned keys. The
// per-box limits are daemon flags, not part of this file.
type DoorConfig struct {
	Enabled            bool     `json:"enabled"`
	DisabledMessage    string   `json:"disabled_message,omitempty"`
	BannedFingerprints []string `json:"banned_fingerprints,omitempty"`
}

// DefaultDoorConfig: open, nothing banned.
func DefaultDoorConfig() DoorConfig { return DoorConfig{Enabled: true} }

// DoorStore keeps DoorConfig in memory and on disk. Reads never touch the
// disk; writes go through atomically (temp file + rename). A file that
// exists but does not parse FAILS CLOSED: the door reports disabled with a
// message naming the problem, so a corrupt or hand-edited state file can
// never silently open the door.
type DoorStore struct {
	path string
	mu   sync.RWMutex
	cfg  DoorConfig
	err  error // non-nil = poisoned (malformed file); every Get reports closed
}

// NewDoorStore loads path. A missing file is the default (open) config;
// a malformed one poisons the store (see DoorStore). path "" keeps the
// store in memory only — fine for tests, wrong for a daemon.
func NewDoorStore(path string) *DoorStore {
	s := &DoorStore{path: path, cfg: DefaultDoorConfig()}
	if path == "" {
		return s
	}
	raw, err := os.ReadFile(path) // #nosec G304 -- path is the daemon's --anon-door-state flag (operator config), never caller input
	switch {
