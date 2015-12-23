package auth

import (
	"crypto/ed25519"
	"crypto/hmac"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// hmacEqualStrings compares two signature strings in constant time. A length
// mismatch can't early-leak: hmac.Equal is fixed-width over the byte slices.
func hmacEqualStrings(a, b string) bool {
	return hmac.Equal([]byte(a), []byte(b))
}

// Asymmetric (ed25519) sentinel→daemon authentication — the #688 successor to
// the symmetric HMAC scheme in sentinel_hmac.go.
//
// WHY: the HMAC secret (CONTAINARIUM_SENTINEL_AUTH_SECRET) is deployment-wide
// and SYMMETRIC — every daemon that must *verify* a sentinel request also holds
// the key needed to *forge* one. On a multi-tenant deployment that is a
// cross-tenant escalation: a BYO-compute (BYOC) host, which only needs to
// accept the sentinel's keysync/certsync, could mint a request that the shared
// workhorse daemon accepts and push attacker-controlled SSH keys into other
// tenants' boxes (/authorized-keys/sentinel).
//
// FIX: the sentinel→daemon direction is always "sentinel signs, daemon
// verifies" (keysync, certsync, the /sentinel/peers response). That is exactly
// what asymmetric signing is for. The sentinel holds an ed25519 PRIVATE key;
// every daemon is given only the PUBLIC key. A daemon (BYOC or not) can verify
// but cannot forge, so distributing the public key everywhere — including to
// untrusted BYOC hosts — carries no escalation.
//
// Wire format reuses the existing timestamp header and overloads the signature
// header with an algorithm tag so a mixed fleet interoperates during migration:
//
//	X-Containarium-Sentinel-Ts:  <unix-seconds>
//	X-Containarium-Sentinel-Sig: ed25519:<base64(signature)>      (this scheme)
//	X-Containarium-Sentinel-Sig: <hex(HMAC-SHA256(...))>          (legacy, no tag)
//
// The canonical signed message is identical to the HMAC scheme — requests sign
// `method "\n" path "\n" ts`, responses sign `body "\n" ts` — so the only thing
// that changes is the primitive, not what is committed to.
//
// The daemon→sentinel PKI bootstrap (/sentinel/ca, /sentinel/peer-cert) is a
// different trust direction with its own threat model and is intentionally NOT
// changed here; it remains on the shared secret pending the mTLS work.

// sentinelEd25519SigPrefix tags an ed25519 signature in the signature header.
// A header value WITHOUT this prefix is treated as a legacy hex HMAC, which is
// how a verifier tells the two apart without a second header.
const sentinelEd25519SigPrefix = "ed25519:"

// ParseSentinelPublicKey decodes a base64 (standard encoding) ed25519 public
// key. This is the value distributed to daemons via
// CONTAINARIUM_SENTINEL_PUBLIC_KEY — safe to ship to any host, including BYOC.
func ParseSentinelPublicKey(b64 string) (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return nil, fmt.Errorf("sentinel public key: not valid base64: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("sentinel public key: want %d bytes, got %d", ed25519.PublicKeySize, len(raw))
	}
	return ed25519.PublicKey(raw), nil
