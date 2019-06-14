package sentinel

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// #2415 — per-backend audit-ingest tokens.
//
// The sentinel's SSH session shipper authenticates to each backend's
// AuditService with a JWT that backend minted for it, scoped to audit:ingest
// only. Those tokens live here, keyed by backend id, in a file separate from
// the tunnel-join token store: the entries have a different shape and a
// different blast radius, and migrating a credential file's schema to share
// one would buy nothing.

// DefaultAuditIngestTokenStorePath is where registered tokens are persisted.
