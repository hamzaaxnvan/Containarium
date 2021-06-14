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
	URI      string            `json:"uri"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// KeyProvider resolves per-tenant key material. Implementations back onto
// whatever custody the operator runs: a file on disk (OSS), or KMS/Vault
// in the cloud control plane.
type KeyProvider interface {
	// Wrap resolves the key for a tenant at container-create time and
	// returns a durable ref for later re-resolution. Calling Wrap twice
	// for the same tenant returns the same key — a tenant's containers
	// share one ZFS encryptionroot.
	Wrap(ctx context.Context, tenantID string) (Key, KeyRef, error)

	// Load re-fetches key material for a previously-issued ref. Used on
	// container start and on migration adopt.
	Load(ctx context.Context, ref KeyRef) (Key, error)
}

// Key holds raw key material.
//
// It is a struct, not a []byte alias, so that String, GoString, and the
// JSON/text marshallers can all be overridden to redact. That closes the
// accidental-disclosure paths that matter in practice: a `%v` in a log
// line, a `%#v` in a debug dump, a struct that happens to get marshalled
// into an API response or an error string.
type Key struct {
	b []byte
}

// NewKey wraps raw bytes, rejecting anything that is not exactly KeyLen.
// The input is copied, so a caller that zeroes or reuses its buffer
// cannot corrupt the key.
func NewKey(b []byte) (Key, error) {
	if len(b) != KeyLen {
		// Deliberately reports only the length. Echoing the rejected
		// input would put key material into an error string, which is
		// exactly what this type exists to prevent.
		return Key{}, fmt.Errorf("key must be exactly %d bytes for ZFS keyformat=raw, got %d", KeyLen, len(b))
	}
