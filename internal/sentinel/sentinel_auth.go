package sentinel

import (
	"crypto/ed25519"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/config"
)

// Sentinel-to-daemon authentication.
//
// Several daemon endpoints (/certs, /authorized-keys,
// /authorized-keys/sentinel) are now HMAC-gated. The shared secret
// lives in CONTAINARIUM_SENTINEL_AUTH_SECRET on both ends. This file
// is the sentinel side: a small helper that builds an HTTP request,
// stamps the auth headers, and returns it. Callers (keysync,
// certsync) replace bare client.Get / client.Post with
// client.Do(newSignedRequest(...)).
//
// The secret is loaded once and cached. A missing or short secret
// triggers a per-call WARNING (rate-limited to once per
// sentinelMisconfigLogInterval) so an operator tailing the journal
// during an SSH-down incident sees the misconfiguration in real
// time, not just in a startup line that may have scrolled away
// (#341).

const sentinelMisconfigLogInterval = 60 * time.Second

var (
	sentinelSecretOnce sync.Once
	sentinelSecret     []byte

	// sentinelSigningKey is the sentinel's ed25519 PRIVATE key (#688),
	// loaded once from CONTAINARIUM_SENTINEL_SIGNING_KEY. When present it
	// is preferred over the HMAC secret for signing outbound requests +
	// responses — daemons verify with only the matching public key and so
	// cannot forge. Nil when unset/invalid (→ HMAC fallback).
	sentinelSigningKeyOnce sync.Once
	sentinelSigningKey     ed25519.PrivateKey

	// lastMisconfigLogNs is the unix-nanos of the last per-call
	// WARNING. atomic.Int64 so concurrent keysync + certsync calls
	// don't double-log every minute.
	lastMisconfigLogNs atomic.Int64
