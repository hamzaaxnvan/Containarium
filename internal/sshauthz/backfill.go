package sshauthz

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SentinelKeyMarker is the comment line the daemon writes before the
// sentinel's upstream key in every jump account's authorized_keys
// (internal/gateway applySentinelKey). The key on the next non-empty line
// is the sentinel's, not a user's, and is never registered. A gateway test
// pins the two constants together.
const SentinelKeyMarker = "# sshpiper sentinel upstream key"

// BackfillStore is the one write the backfill needs.
type BackfillStore interface {
	InsertIfAbsent(ctx context.Context, rec KeyRecord) (bool, error)
}

// UnreadableAccount is an account whose authorized_keys the scan could not
// read as a plain file: a symlinked home, .ssh or authorized_keys, or an
// open or read error other than "does not exist". What keys it holds is
// unknown, so a caller must not treat it as holding none (#3241).
type UnreadableAccount struct {
	Username string
	Err      error
}

// ScanResult is what ScanAuthorizedKeys found. One bad account is reported in
// Unreadable and never fails the scan, so the other accounts stay usable.
type ScanResult struct {
	Records    []KeyRecord
	Unreadable []UnreadableAccount
}

// ScanAuthorizedKeys reads homeRoot/*/.ssh/authorized_keys and returns one
// backfill record per user key, created at now. Comments, blank lines,
// unparseable lines and the sentinel's marked block are skipped. An account
// with no authorized_keys holds nothing; an account whose home, .ssh or
// authorized_keys is a symlink, or cannot be opened or read, is listed in
// Unreadable and contributes no records. Only an unreadable homeRoot is an
// error.
func ScanAuthorizedKeys(homeRoot string, now time.Time) (ScanResult, error) {
	entries, err := os.ReadDir(homeRoot)
	if err != nil {
		return ScanResult{}, fmt.Errorf("read %s: %w", homeRoot, err)
	}
