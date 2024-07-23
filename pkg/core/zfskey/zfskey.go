// Package zfskey provides per-tenant key custody for ZFS native
// encryption.
//
// It implements phase 1 of docs/ZFS-PER-CONTAINER-ENCRYPTION-DESIGN.md:
// the pluggable KeyProvider interface (§2), the file-based reference
// implementation, and the in-memory key cache (§4). Nothing here talks to
// ZFS — the lifecycle hooks that do live in a later phase (#1199, #1201).
//
// The one rule the whole package exists to enforce: key material lives in
// process memory and in operator-controlled files, and nowhere else. It is
// never logged, never serialised, and never written to disk by this
// package. Key is a struct rather than a []byte precisely so that rule is
// enforced by the type system instead of by reviewer vigilance.
package zfskey

import (
	"context"
	"fmt"
)

// KeyLen is the key size ZFS native encryption requires for
// `keyformat=raw`. A key of any other length is rejected at the boundary
// rather than surfacing later as an opaque `zfs load-key` failure.
const KeyLen = 32

// Scheme identifies which custody backend a KeyRef points at.
type Scheme string

const (
	// SchemeFile is the OSS reference implementation: a raw key file on
	// the daemon host.
	SchemeFile Scheme = "file"
)

// KeyRef is a durable pointer to key material. It is stored on container
// metadata so a restarted daemon — or the destination of a migration —
// can re-resolve the same key. It deliberately carries no key bytes: a
// KeyRef is safe to log, persist, and send over the wire.
type KeyRef struct {
	Scheme   Scheme            `json:"scheme"`
