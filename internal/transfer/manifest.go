package transfer

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// fileEntry is one row in a manifest — a content hash and the file's mode.
// Size and mtime aren't recorded because they don't change the diff
// outcome (sha catches all content changes; mode catches the rest).
type fileEntry struct {
	Path string // relative to the manifest root, forward-slash-separated
	Hash string // hex-encoded sha256 of file content
	Mode uint32 // os.FileMode bits we care about: executable + special
}

// manifest is a sorted-by-path map of entries; comparison is straightforward.
type manifest struct {
	entries map[string]fileEntry
}

func newManifest() *manifest {
	return &manifest{entries: map[string]fileEntry{}}
}

// walkLocal builds a manifest for a local directory, skipping any path
// whose forward-slash form matches one of the excludes (substring match).
