package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"
)

// Sentinel↔daemon HMAC authentication.
//
// Several daemon endpoints (`/authorized-keys`, `/authorized-keys/sentinel`,
// `/certs`) are called by the sentinel and were previously
// unauthenticated under the comment "VPC-only". That assumption fails
// closed badly: a single firewall misconfiguration or VPC-peering
// change exposes every container's SSH public-key inventory and the
// platform's TLS certificates.
//
// Until full sentinel-to-daemon mTLS lands (Phase 0.5), we gate these
// endpoints with a shared-secret HMAC over a canonical request
// string. The sentinel signs, the daemon verifies. Stronger than the
// "trust the LAN" model, weaker than mTLS — but a forward-compatible
// step.
//
// Wire format:
//
//	X-Containarium-Sentinel-Ts:  <unix-seconds>
//	X-Containarium-Sentinel-Sig: <hex(HMAC-SHA256(secret, method "\n" path "\n" ts))>
//
// Verification rejects:
//   - missing or malformed headers
//   - timestamp outside ±300s of server clock (replay-window cap)
//   - signature mismatch (constant-time compare)
//
// The secret comes from CONTAINARIUM_SENTINEL_AUTH_SECRET (mirrored on
// both ends). 32-byte minimum length is enforced at config-load time.

const (
	SentinelHeaderTimestamp = "X-Containarium-Sentinel-Ts"
	SentinelHeaderSignature = "X-Containarium-Sentinel-Sig"

	// SentinelMaxClockSkew bounds the timestamp tolerance for
	// replay protection. 5 minutes is wide enough for NTP drift and
	// laptop-time mistakes on operator boxes, tight enough that a
	// captured request stops working before an attacker can replay
	// it for any meaningful blast radius.
	SentinelMaxClockSkew = 5 * time.Minute

	// SentinelMinSecretLen is the smallest acceptable shared
	// secret. HMAC-SHA256 expects a key at least as long as its
	// block size for the security level it claims; 32 bytes is
	// also the floor most secret managers default to.
	SentinelMinSecretLen = 32
)

// Response signing. Counterpart of the request-signing helpers
// above, used when the sentinel returns data the daemon needs to
// trust — currently /sentinel/peers (the peer-discovery response).
//
// Without this, a compromised sentinel (or active MITM on the path)
// can inject attacker-controlled peer URLs and the daemon will
// proxy container management traffic to them. See finding
// C-CRIT-2.
//
// Wire format reuses the same headers as request signing — same
// secret, same timestamp window, same algorithm — but the canonical
// string is built from the response body bytes plus the timestamp:
//
//	signature = HMAC-SHA256(secret, body "\n" ts)
//
// Method and path are not mixed in (the response carries no method
// of its own), so callers must ensure the response shape itself
// commits to whatever context matters. For /sentinel/peers the
// body is a JSON object containing the peer ID + proxy_path, which
// is sufficient for the daemon to tell which peers to trust.

// SignSentinelRequest stamps the timestamp + signature headers onto
// `req`. Used by the sentinel-side HTTP client before sending. The
// secret must satisfy SentinelMinSecretLen (caller responsibility).
func SignSentinelRequest(req *http.Request, secret []byte) {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	sig := computeSentinelSignature(secret, req.Method, req.URL.Path, ts)
	req.Header.Set(SentinelHeaderTimestamp, ts)
	req.Header.Set(SentinelHeaderSignature, sig)
}

// VerifySentinelRequest returns nil if `req` carries a valid
// sentinel-HMAC signature for `secret`. The error is intentionally
