package sentinel

import (
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// certForKeyWithValidity builds a self-signed certificate over the identity's
// own key with the given validity window, to prove the pin verifier looks at
// the key only and never at the dates.
func certForKeyWithValidity(t *testing.T, id *TunnelIdentity, notBefore, notAfter time.Time) []byte {
	t.Helper()
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(42),
		Subject:      id.cert.Subject,
		NotBefore:    notBefore,
		NotAfter:     notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &id.key.PublicKey, id.key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	return der
}

func TestTunnelIdentityPinRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tunnel-identity.pem")

	id, err := GenerateTunnelIdentity()
	if err != nil {
		t.Fatalf("GenerateTunnelIdentity: %v", err)
	}
	pin := id.Pin()
	if !strings.HasPrefix(string(pin), "sha256:") || len(pin) != len("sha256:")+64 {
		t.Fatalf("pin %q is not sha256:<64 hex>", pin)
	}
	if id.Pin() != pin {
		t.Fatalf("pin not stable across calls")
	}

	if err := id.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := st.Mode().Perm(); mode != 0o600 {
		t.Fatalf("identity file mode = %o, want 600", mode)
	}

	loaded, err := LoadTunnelIdentity(path)
	if err != nil {
		t.Fatalf("LoadTunnelIdentity: %v", err)
	}
	if loaded.Pin() != pin {
		t.Fatalf("pin after load = %q, want %q", loaded.Pin(), pin)
	}

	// The pin must be parseable by the client-side parser.
	parsed, err := ParsePins(string(pin))
	if err != nil {
		t.Fatalf("ParsePins(own pin): %v", err)
	}
	verify := VerifyPinned(parsed)

	t.Run("accepts matching SPKI", func(t *testing.T) {
		if err := verify(loaded.TLSCertificate().Certificate, nil); err != nil {
			t.Fatalf("verifier rejected matching identity: %v", err)
		}
	})

	t.Run("rejects different key with same subject", func(t *testing.T) {
		other, err := GenerateTunnelIdentity()
		if err != nil {
			t.Fatalf("GenerateTunnelIdentity: %v", err)
		}
		if other.cert.Subject.String() != id.cert.Subject.String() {
			t.Fatalf("test precondition: subjects differ (%q vs %q)", other.cert.Subject, id.cert.Subject)
		}
		if err := verify(other.TLSCertificate().Certificate, nil); err == nil {
			t.Fatalf("verifier accepted a different key with the same subject")
		}
	})

	t.Run("ignores validity dates", func(t *testing.T) {
		expired := certForKeyWithValidity(t, id, time.Now().Add(-48*time.Hour), time.Now().Add(-24*time.Hour))
		if err := verify([][]byte{expired}, nil); err != nil {
			t.Fatalf("verifier rejected expired cert with pinned key: %v", err)
		}
		notYet := certForKeyWithValidity(t, id, time.Now().Add(24*time.Hour), time.Now().Add(48*time.Hour))
		if err := verify([][]byte{notYet}, nil); err != nil {
			t.Fatalf("verifier rejected not-yet-valid cert with pinned key: %v", err)
		}
	})
}

func TestTunnelIdentitySaveTightensExistingFileMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "id.pem")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil { //nolint:gosec // test fixture with deliberately loose mode
		t.Fatal(err)
	}
	id, err := GenerateTunnelIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if err := id.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := st.Mode().Perm(); mode != 0o600 {
		t.Fatalf("mode = %o, want 600", mode)
	}
}

func TestLoadOrGenerateTunnelIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "id.pem")

	first, generated, err := LoadOrGenerateTunnelIdentity(path)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if !generated {
		t.Fatalf("first call should generate")
	}
	second, generated, err := LoadOrGenerateTunnelIdentity(path)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if generated {
		t.Fatalf("second call should load, not generate")
	}
	if first.Pin() != second.Pin() {
		t.Fatalf("pin changed across restarts: %q vs %q", first.Pin(), second.Pin())
	}

	// A present but corrupt file is an error, never silently replaced:
	// regenerating would invalidate every client's pin.
	if err := os.WriteFile(path, []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadOrGenerateTunnelIdentity(path); err == nil {
		t.Fatalf("corrupt identity file must not be silently regenerated")
	}
}

func TestLoadTunnelIdentityRejectsBadFiles(t *testing.T) {
	a, err := GenerateTunnelIdentity()
	if err != nil {
		t.Fatal(err)
