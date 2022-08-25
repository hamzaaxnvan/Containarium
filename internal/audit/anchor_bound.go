package audit

import (
	"context"
	"fmt"
)

// PurgeAnchor is the newest anchor a retention purge may stay below (#2878):
// the purge removes rows strictly under Checkpoint and keeps the anchored row,
// so verify-anchor can still compare it with the database afterwards.
type PurgeAnchor struct {
	Checkpoint int64
	Root       string
}

// SafeAnchor picks the anchor that bounds a purge. With only a local file it is
// the file's latest record. With an external sink as well it is the LOWER of the
// two latest checkpoints, so neither copy can be the one that lets rows go: an
// object planted above the real tip in a bucket raises only the bucket's
// "latest", and the file still holds the bound down; a bucket that is behind
