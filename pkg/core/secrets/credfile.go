package secrets

import (
	"fmt"
	"os"
	"strings"
)

// readBearerLikeFile reads a credential file with the same perm contract as the
// JWT / Postgres secret files: mode must be ≤ 0600. Whitespace trimmed.
//
// The GCP KMS backend re-reads its token file via this helper before each call
// so an out-of-band refresh takes effect without a daemon restart (#300). The
// app-layer KMS factory (internal/secrets) keeps its own copy for load-time
// resolution — the contract is duplicated by intent so each secret-file reader
// stays self-contained.
func readBearerLikeFile(path string) (string, error) {
