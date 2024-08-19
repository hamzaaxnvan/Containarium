package sentinel

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
)

// Handshake v2 is the shape a tunnel client sends inside a TLS session. It
// carries no token. Instead it names the token by a short id and proves
// possession of it with an HMAC over keying material exported from the
// TLS session (RFC 8446 §7.5), so the proof is only meaningful to the two
// ends of that one session.
const (
	tunnelHandshakeV2     = 2
	tunnelTokenIDLen      = 16
	tunnelTokenProofLabel = "containarium-tunnel-token-proof/1" // #nosec G101 -- a keying-material export label, not a credential value
	tunnelTokenProofLen   = 32
)

// tunnelTokenID is the public name of a token: the first 16 hex characters
// of its SHA-256. It lets the policy find the token without the client
// sending it.
func tunnelTokenID(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])[:tunnelTokenIDLen]
}

// tunnelTokenProof is base64(HMAC-SHA256(key = token, msg = ekm)).
func tunnelTokenProof(token string, ekm []byte) string {
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write(ekm)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// tunnelTokenEKM exports the per-session keying material both peers derive
// from the same TLS session, which the proof is computed over.
func tunnelTokenEKM(cs tls.ConnectionState) ([]byte, error) {
	ekm, err := cs.ExportKeyingMaterial(tunnelTokenProofLabel, nil, tunnelTokenProofLen)
	if err != nil {
		return nil, fmt.Errorf("export tunnel keying material: %w", err)
	}
	return ekm, nil
}

// TokenPolicy maps tunnel tokens to the set of pools each token is allowed
// to join. Used by validateHandshakeV2 to reject pool spoofing — a token
// restricted to "lab" cannot register a tunnel claiming pool="prod".
//
// Special pool values:
//
//	"*" — matches any pool (including the empty/unpooled case). Used for
//	      legacy single-token deployments where pool isn't a security
//	      boundary.
//	""  — matches the unpooled/legacy backend explicitly.
type TokenPolicy struct {
	mu    sync.RWMutex
	rules map[string][]Pool
	// byID indexes rules by tunnelTokenID so a v2 handshake, which names
	// the token by id only, resolves to the token(s) to check its proof
	// against. Normally one token per id.
	byID map[string][]string
}

// NewTokenPolicy returns an empty policy. With no entries, every handshake
