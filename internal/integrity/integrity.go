// Package integrity turns a backend's own runtime state into a signed
// self-measurement that the control plane verifies out of band of the
// backend's normal reporting, to detect tampering of a backend's control
// plane.
//
// The measurement hashes three things:
//   - the running daemon binary on disk,
//   - the loaded in-kernel network-policy program object(s), and
//   - the canonical policy/config state (the integrity-relevant daemon
//     configuration + active network-policy posture).
//
// Those component digests are folded into one canonical byte string, hashed
// once more to a measurement digest, and signed with the node's identity key.
// The signer is supplied by the caller: the daemon reuses the sentinel-issued
// peer leaf private key from the peer-PKI plumbing (TPM-backed when a TPM is
// present, software-signed via the peer key otherwise). When no identity key
// is available the measurement is still produced, just unsigned — the control
// plane treats an unsigned measurement as unverifiable, not as tamper.
//
// The compute here is pure (no proto, no Incus, no filesystem assumptions
// beyond reading the paths the caller hands it) so it is unit-testable in
// isolation; the server gathers the inputs (binary path, program-object
// paths, config snapshot, signer) and maps the result onto the wire type.
// This is the node-side half only; the control plane's verification and
// quarantine logic lives elsewhere. See #683.
package integrity

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

// HashAlgorithm is the digest algorithm name recorded in the measurement. Only
// sha256 is produced today; the field makes the choice explicit on the wire so
// a future change is unambiguous to the verifier.
const HashAlgorithm = "sha256"

// SignatureAlgorithmECDSAP256SHA256 is the scheme recorded when an ECDSA P-256
// identity key (the sentinel-issued peer leaf) signs the measurement.
const SignatureAlgorithmECDSAP256SHA256 = "ecdsa-p256-sha256"

// ProgramObject identifies one in-kernel program object to digest: a stable
// name (the configured object path) and the path to read its bytes from.
type ProgramObject struct {
	// Name is the stable identifier recorded in the measurement (typically the
	// configured object path itself).
	Name string
	// Path is where to read the object bytes from. When empty, Name is used as
	// the path.
	Path string
	// Data, when non-nil, holds the object bytes in memory (the object embedded
	// in the binary has no file on disk, #3079). It takes precedence over Path
	// and Name; the digest is the same SHA-256 of the object bytes either way.
	Data []byte
}

// Inputs is the snapshot the measurement compute consumes. The caller (the
// daemon) gathers it: the running binary path, the loaded program objects, a
// canonicalizable config-state snapshot, the daemon version, and a signer.
type Inputs struct {
	// BinaryPath is the path to the running daemon binary on disk. Empty skips
	// the binary digest (recorded as empty).
	BinaryPath string

	// Programs are the loaded in-kernel program object(s) to digest. Empty when
	// no program object is configured on this backend.
	Programs []ProgramObject

	// ConfigState is the integrity-relevant policy/config snapshot. It is
	// serialized canonically (sorted keys) before hashing so two daemons with
	// the same posture produce the same digest regardless of map ordering. Nil
	// is treated as an empty object.
	ConfigState map[string]string

	// DaemonVersion is recorded into the measurement.
	DaemonVersion string

	// Signer is the node identity key. When nil the measurement is produced
	// unsigned. Today the daemon passes its sentinel-issued peer leaf key
	// (an *ecdsa.PrivateKey via tls.Certificate.PrivateKey).
	Signer crypto.Signer

