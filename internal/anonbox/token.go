package anonbox

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The claim token (design §Contracts "Claim token", #2199):
//
//	v1.<box_name>.<fp_hash>.<exp_unix>.<token_id>.<hmac>
//
// where hmac = HMAC-SHA256(secret, "v1.<box_name>.<fp_hash>.<exp_unix>.<token_id>")
// and secret = TokenManager.DeriveSharedSecret("anon-claim", box_name).
//
// The token is stored nowhere but inside the guest; everything needed to
// verify it is the box's own labels (fp_hash, claim_token_id, claimed_at)
// plus a secret the daemon re-derives from its signing key on demand. A
// '.' can occur in none of the fields (box names are [a-z0-9-], the rest
// hex/decimal), so the split is unambiguous.

// TokenVersion is the leading field.
const TokenVersion = "v1"

// ClaimToken is a parsed token, before or after verification.
type ClaimToken struct {
	BoxName   string
	FPHash    string
	ExpiresAt time.Time
	TokenID   string
}

var (
	// ErrTokenMalformed: not six fields, wrong version, or a field that
	// does not parse.
	ErrTokenMalformed = errors.New("anonbox: malformed claim token")
	// ErrTokenSignature: the HMAC does not verify under the box's secret.
	ErrTokenSignature = errors.New("anonbox: claim token signature invalid")
	// ErrTokenExpired: exp has passed.
	ErrTokenExpired = errors.New("anonbox: claim token expired")
)

// MintClaimToken signs t under secret. An empty secret is refused: a token
// signed with "" would verify under "" everywhere.
func MintClaimToken(secret string, t ClaimToken) (string, error) {
	if secret == "" {
		return "", errors.New("anonbox: mint claim token: empty secret")
	}
	if t.BoxName == "" || t.FPHash == "" || t.TokenID == "" || t.ExpiresAt.IsZero() {
		return "", errors.New("anonbox: mint claim token: box, fp_hash, token_id and expiry are required")
	}
