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

// KeyResolver returns the REAL upstream key for one key owner and provider.
//
// Implementations are injected through Config; the gateway never reads a key
// out of the environment itself after construction. The daemon satisfies this
// with its existing encrypted secrets store, which keeps per-owner keys under a
// reserved namespace that no tenant can list and no delivery mode ever ships to
// a box.
//
// Implementations must be safe for concurrent use — this is consulted on the
// hot path of every proxied model call whose token carries a key_owner.
type KeyResolver interface {
	// KeyFor returns the key for (keyOwner, provider) and whether one exists.
	// It must return ("", false) rather than an error for "no key here": the
	// gateway's fallback to the daemon-global key is a normal outcome, not a
	// failure worth failing a request over.
	KeyFor(ctx context.Context, keyOwner, provider string) (key string, ok bool)
}

// KeyResolverFunc adapts a function to KeyResolver.
type KeyResolverFunc func(ctx context.Context, keyOwner, provider string) (string, bool)

// KeyFor implements KeyResolver.
func (f KeyResolverFunc) KeyFor(ctx context.Context, keyOwner, provider string) (string, bool) {
	return f(ctx, keyOwner, provider)
}

// resolveKey picks the real upstream key for one call.
//
// Precedence, per the design ("workspace on your own inference key", component
// 1): the token's own owner first, the daemon-global key second.
//
//  1. no key_owner claim -> Config.ProviderKeys only. The resolver is not even
//     asked, so a legacy token behaves bit-for-bit as before.
//  2. key_owner + a resolver that has a key -> that key.
//  3. key_owner + no key for that owner -> Config.ProviderKeys, logged. The
//     design specifies this fallback; it is also the case worth noticing,
//     because an owner whose key was removed would otherwise silently spend
//     the platform's. The mint path refusing to issue a key_owner token
//     without a key (FootprintAI/Containarium#1726) is what keeps case 3 from
//     happening in normal operation. With Config.DisableGlobalKeyFallback the
//     fallback does not exist: a key_owner token resolves through its owner or
//     not at all, whether or not a resolver is wired.
//  4. neither -> ("", false), and handleModel refuses the call without ever
//     touching an upstream.
func (g *Gateway) resolveKey(ctx context.Context, claims *GatewayClaims, provider string) (string, bool) {
	key, ok, _ := g.resolveKeyOwner(ctx, claims, provider)
	return key, ok
}

// resolveKeyOwner is resolveKey that also says whose key it returned, for the
// run's call log (#2928): the owner's own (ORG or MEMBER by the namespace of
// key_owner), or the operator's when the daemon-global key stood in. The owner
// is meaningful only when ok.
func (g *Gateway) resolveKeyOwner(ctx context.Context, claims *GatewayClaims, provider string) (string, bool, pb.GatewayKeyOwner) {
	if claims.KeyOwner != "" && g.cfg.DisableGlobalKeyFallback {
		if g.cfg.KeyResolver != nil {
			if key, ok := g.cfg.KeyResolver.KeyFor(ctx, claims.KeyOwner, provider); ok && key != "" {
				return key, true, ownerKind(claims.KeyOwner)
			}
		}
		return "", false, pb.GatewayKeyOwner_GATEWAY_KEY_OWNER_UNSPECIFIED
	}
	if claims.KeyOwner != "" && g.cfg.KeyResolver != nil {
		if key, ok := g.cfg.KeyResolver.KeyFor(ctx, claims.KeyOwner, provider); ok && key != "" {
			return key, true, ownerKind(claims.KeyOwner)
		}
		if global := g.cfg.ProviderKeys[provider]; global != "" {
			g.cfg.Logger.Printf("model-gateway: key_owner=%s has no %s key; falling back to the daemon-global key (this call is billed to the operator, not the owner)",
				claims.KeyOwner, provider)
			return global, true, pb.GatewayKeyOwner_GATEWAY_KEY_OWNER_OPERATOR_FALLBACK
		}
		return "", false, pb.GatewayKeyOwner_GATEWAY_KEY_OWNER_UNSPECIFIED
	}
	key := g.cfg.ProviderKeys[provider]
	return key, key != "", pb.GatewayKeyOwner_GATEWAY_KEY_OWNER_OPERATOR_FALLBACK
}

