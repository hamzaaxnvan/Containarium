package modelgateway

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Per-owner key resolution — whose upstream key a proxied call spends.
//
// The gateway used to hold exactly one real key per provider for the whole
// daemon (Config.ProviderKeys). That is right for a single-operator install and
// wrong the moment one daemon serves several owners, each paying for their own
// upstream: the key that gets injected has to be a property of the *token*, not
// of the process.
//
// So a token may carry a `key_owner` claim, and the gateway resolves
// (key_owner, provider) -> real key through an injected KeyResolver, falling
// back to the daemon-global key when the owner has none. A token WITHOUT the
// claim never reaches the resolver and resolves exactly as it did before, which
// is what keeps every already-issued skill-box and recipe-box token working.
//
// The owner namespaces are deliberately prefixed. A self-hosted daemon's
// usernames are arbitrary user-chosen strings and cloud org ids are UUIDs drawn
// from the same string space; without a discriminator a username crafted to
// match a target org's id would resolve to that org's key under the same
// lookup. `user:` / `org:` makes the two spaces disjoint by construction.

// Key-owner namespace prefixes. Load-bearing, not decorative — see above.
const (
	// KeyOwnerUserPrefix namespaces a self-hosted daemon's local username.
	KeyOwnerUserPrefix = "user:"
	// KeyOwnerOrgPrefix namespaces a cloud organization id.
	KeyOwnerOrgPrefix = "org:"
)

// UserKeyOwner is the key_owner for a self-hosted box owned by username.
func UserKeyOwner(username string) string { return KeyOwnerUserPrefix + username }

// OrgKeyOwner is the key_owner for a box attributed to a cloud organization.
func OrgKeyOwner(orgID string) string { return KeyOwnerOrgPrefix + orgID }

// keyOwnerIDRE is what may follow the namespace prefix: the printable,
// separator-free shapes a username or a UUID takes. It deliberately excludes
// '/', ':' and whitespace so a key_owner can never break out of the namespace
// it is used to build (e.g. the reserved secret-store path the daemon keeps
// per-owner keys under).
var keyOwnerIDRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ValidateKeyOwner returns nil for a well-formed key_owner: one of the known
// namespace prefixes followed by an id of printable, separator-free characters.
// Callers that mint tokens or store per-owner keys validate here rather than
// each inventing a rule.
func ValidateKeyOwner(keyOwner string) error {
	if keyOwner == "" {
		return fmt.Errorf("modelgateway: key_owner is required")
	}
	for _, prefix := range []string{KeyOwnerUserPrefix, KeyOwnerOrgPrefix} {
		id, ok := strings.CutPrefix(keyOwner, prefix)
		if !ok {
			continue
		}
		if !keyOwnerIDRE.MatchString(id) {
			return fmt.Errorf("modelgateway: key_owner %q: id after %q must match %s", keyOwner, prefix, keyOwnerIDRE)
		}
		return nil
	}
	return fmt.Errorf("modelgateway: key_owner %q must start with %q or %q", keyOwner, KeyOwnerUserPrefix, KeyOwnerOrgPrefix)
}

