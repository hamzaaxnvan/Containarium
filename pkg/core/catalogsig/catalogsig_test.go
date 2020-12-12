package catalogsig

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func genKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return pub, priv
}

// writeKeyFile writes a trusted-keys file with the given public keys, base64 one
// per line, plus a comment and a blank line to exercise the parser.
func writeKeyFile(t *testing.T, dir string, pubs ...ed25519.PublicKey) string {
	t.Helper()
	path := filepath.Join(dir, "trusted.keys")
	body := "# trusted catalog keys\n\n"
	for _, p := range pubs {
		body += base64.StdEncoding.EncodeToString(p) + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write key file: %v", err)
	}
	return path
}

func TestVerifyAcceptsGoodSignature(t *testing.T) {
	pub, priv := genKey(t)
	v := &Verifier{keys: []ed25519.PublicKey{pub}}
	data := []byte("skills:\n  - id: x\n")
	if err := v.Verify(data, ed25519.Sign(priv, data)); err != nil {
		t.Fatalf("Verify good sig: %v", err)
	}
}

func TestVerifyRejectsTamperedDataAndWrongKey(t *testing.T) {
	pub, priv := genKey(t)
	_, otherPriv := genKey(t)
	v := &Verifier{keys: []ed25519.PublicKey{pub}}
	data := []byte("skills:\n  - id: x\n")

	// Tampered payload, valid-shape signature over the original.
	if err := v.Verify([]byte("skills:\n  - id: evil\n"), ed25519.Sign(priv, data)); err == nil {
		t.Fatal("expected tampered data to fail verification")
	}
	// Right data, signed by an untrusted key.
	if err := v.Verify(data, ed25519.Sign(otherPriv, data)); err == nil {
		t.Fatal("expected untrusted-key signature to fail verification")
	}
	// Wrong-size signature.
	if err := v.Verify(data, []byte("short")); err == nil {
		t.Fatal("expected wrong-size signature to fail verification")
	}
}

func TestVerifyMultipleTrustedKeysAllowsRotation(t *testing.T) {
	oldPub, _ := genKey(t)
	newPub, newPriv := genKey(t)
	v := &Verifier{keys: []ed25519.PublicKey{oldPub, newPub}}
	data := []byte("crews:\n  - id: c\n")
	if err := v.Verify(data, ed25519.Sign(newPriv, data)); err != nil {
		t.Fatalf("rotation: signature under one trusted key should verify: %v", err)
	}
}

func TestLoadVerifierRoundTrip(t *testing.T) {
	dir := t.TempDir()
	pub, priv := genKey(t)
	keyPath := writeKeyFile(t, dir, pub)

	v, err := LoadVerifier(keyPath)
	if err != nil {
		t.Fatalf("LoadVerifier: %v", err)
	}
	data := []byte("hello")
	if err := v.Verify(data, ed25519.Sign(priv, data)); err != nil {
		t.Fatalf("Verify after LoadVerifier: %v", err)
	}
}

func TestLoadVerifierRejectsBadKeyFile(t *testing.T) {
	dir := t.TempDir()
