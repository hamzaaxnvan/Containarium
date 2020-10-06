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

