// Package credentials manages the on-disk store of per-server CLI
// auth tokens at ~/.containarium/credentials.json, populated by
// `containarium login` and consumed by the rest of the CLI when no
// explicit --token / CONTAINARIUM_TOKEN is set.
//
// See prd/cloud/cli-login-and-multi-env-ssh.md §"Credentials file
// format" for the locked schema. Multi-server: a user can be logged
// into the hosted cloud and one or more self-hosted instances
// simultaneously.
//
// Concurrency: the file is small (<1 KB typical), per-user, and
// touched only by interactive CLI invocations. We rely on the
// 0600 perm bits plus the atomic-rename Save below; we do NOT use
// file locking. If two `login` runs race, the second wins.
package credentials

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DefaultRelPath is the path of the credentials file relative to the
// user's home directory. Exported so tests and the CLI can refer to
// the same constant.
const DefaultRelPath = ".containarium/credentials.json"

// ServerCreds is the per-server credential record persisted inside
// CredentialsFile.Servers. Fields are pointer-free where possible so
// the JSON round-trip is loss-less.
//
// ExpiresAt is a *time.Time because the locked schema explicitly
// allows `null` (non-expiring tokens — service accounts, dev tokens).
// Marshalling nil emits JSON `null`, matching the PRD example.
// AccessModel is how a server grants box access. It's a closed set, so it's a
// defined type with typed constants rather than a bare string (per the repo's
// strong-typing convention).
type AccessModel string

const (
	// AccessModelToken — cloud: the API token IS the credential
	// (`containarium connect`), no per-user SSH key.
	AccessModelToken AccessModel = "token"
	// AccessModelSSHKey — self-hosted: register the user's SSH public key.
	AccessModelSSHKey AccessModel = "sshKey"
)

// Known reports whether m is one of the defined access models (vs an empty or
// unrecognized value decoded off the wire).
func (m AccessModel) Known() bool {
	return m == AccessModelToken || m == AccessModelSSHKey
}

// APITokenPrefix is the prefix the hosted control plane mints its API tokens
// with. It is a ONE-WAY target signal: only the control plane issues it, so a
// credential carrying it unambiguously targets the cloud. A bare JWT (`eyJ…`)
// is ambiguous — a self-hosted daemon and the cloud both accept JWTs — so it
// is NOT a cloud signal on its own (fall back to the stored AccessModel).
//
// Single source of truth for both the MCP backend classifier and the CLI's
// target-aware command guards.
const APITokenPrefix = "ctnr_"

// IsCloudToken reports whether a credential is a hosted-control-plane API key,
// purely from its shape (the APITokenPrefix). One-way: true ⇒ cloud; false ⇒
// unknown (could be a daemon JWT or a cloud JWT), so callers that have the
// server URL should also consult the cached AccessModel.
func IsCloudToken(token string) bool {
	return strings.HasPrefix(strings.TrimSpace(token), APITokenPrefix)
}
