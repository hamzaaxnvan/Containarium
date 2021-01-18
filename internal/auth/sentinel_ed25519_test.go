package auth

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func mustKeypair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return pub, priv
}

func newSignedReq(t *testing.T, priv ed25519.PrivateKey, method, path string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, "http://daemon"+path, nil)
	SignSentinelRequestEd25519(req, priv)
	return req
}

// validHMACSecret is a deterministic >=32-byte secret for the legacy path.
var validHMACSecret = []byte("0123456789abcdef0123456789abcdef")

func TestParseSentinelKeys(t *testing.T) {
	pub, priv := mustKeypair(t)

	pubB64 := base64.StdEncoding.EncodeToString(pub)
	gotPub, err := ParseSentinelPublicKey("  " + pubB64 + "\n")
	if err != nil || !bytes.Equal(gotPub, pub) {
		t.Fatalf("ParseSentinelPublicKey round-trip failed: err=%v equal=%v", err, bytes.Equal(gotPub, pub))
	}

	// Full 64-byte private key round-trips.
	gotPriv, err := ParseSentinelSigningKey(base64.StdEncoding.EncodeToString(priv))
	if err != nil || !bytes.Equal(gotPriv, priv) {
		t.Fatalf("ParseSentinelSigningKey (full) failed: err=%v", err)
	}

	// 32-byte seed expands to the same key.
	seedPriv, err := ParseSentinelSigningKey(base64.StdEncoding.EncodeToString(priv.Seed()))
	if err != nil || !bytes.Equal(seedPriv, priv) {
		t.Fatalf("ParseSentinelSigningKey (seed) failed: err=%v", err)
	}

	// Failure cases.
	if _, err := ParseSentinelPublicKey("not-base64!!"); err == nil {
		t.Error("expected error on non-base64 public key")
	}
	if _, err := ParseSentinelPublicKey(base64.StdEncoding.EncodeToString([]byte("too short"))); err == nil {
		t.Error("expected error on wrong-length public key")
