package guardrail

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Placeholder tokens look like [[EMAIL:1a2b3c4d5e6f]]: the engine's type
// name, then 12 hex characters of an HMAC over (kind, type, value) under a
// key that never leaves the side that ran the guardrail. Same value, same
// token — joins across documents survive — while the token alone says
// nothing about the value. The format is also what the gate recognises as
// "already redacted" when it re-scans (see placeholderRE).
var placeholderRE = regexp.MustCompile(`\[\[[A-Z0-9_]+:[0-9a-f]{12}\]\]`)

// VaultEntry is the original behind one token. The vault is the only
// re-identification path; it stays with the key, on the data owner's side.
type VaultEntry struct {
	Kind  pb.GuardrailKind `json:"kind"`
	Type  string           `json:"type"`
	Value string           `json:"value"`
}

// Vault maps placeholder token -> original. SECRET findings are redacted
// but never vaulted: a persisted store of credentials, however encrypted,
// is a risk class this flow does not take on.
type Vault map[string]VaultEntry

// RedactResult is what Redact produced for one run.
type RedactResult struct {
	// Wiped text per unit id, including units that had no findings.
	Wiped map[string]string
	Vault Vault
	// Findings on the original, per kind, before any action.
	Found map[pb.GuardrailKind]int64
	// Findings whose kind the policy marks BLOCK. Any non-zero count fails
