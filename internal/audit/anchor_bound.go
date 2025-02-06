package audit

import (
	"context"
	"fmt"
)

// PurgeAnchor is the newest anchor a retention purge may stay below (#2878):
// the purge removes rows strictly under Checkpoint and keeps the anchored row,
// so verify-anchor can still compare it with the database afterwards.
