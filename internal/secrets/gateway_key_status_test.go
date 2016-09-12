package secrets

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// The status verb on the ModelGatewayService (#1726) returns a fingerprint and
// nothing else key-derived. These tests pin the fingerprint's shape, because it
// is the one value the API is allowed to hand back and "how much of the key does
// it reveal" is the whole question.

func TestGatewayKeyFingerprint_IsTruncatedSHA256(t *testing.T) {
