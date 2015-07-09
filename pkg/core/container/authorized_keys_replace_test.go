package container

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

const replaceTestKey3 = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGq6JPuM6JlU+5s2m0oWz8aUo6d9lBn3pq1tH0v3b5jK test3"

// sentinelBlock is how the sentinel's upstream key sits in a box's file.
const sentinelBlock = SentinelKeyMarker + "\n" + "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPlatformSentinelKeyMaterial000000000000000 sentinel\n"

func replaceFixture(t *testing.T, content string) (user, path string) {
	t.Helper()
	tmp := t.TempDir()
	withTestHomeRoot(t, tmp, func(string) bool { return true })
	user = "boxuser"
	path = filepath.Join(tmp, user, ".ssh", "authorized_keys")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return user, path
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestReplaceAuthorizedKeys_SetThenShrink(t *testing.T) {
	user, path := replaceFixture(t, "")
	if _, err := ReplaceAuthorizedKeys(user, []string{validTestKey1, validTestKey2}, ReplaceHooks{}); err != nil {
		t.Fatal(err)
	}
	removed, err := ReplaceAuthorizedKeys(user, []string{validTestKey2}, ReplaceHooks{})
	if err != nil {
		t.Fatal(err)
	}
	got := readFile(t, path)
	if strings.Contains(got, validTestKey1) || !strings.Contains(got, validTestKey2) {
		t.Fatalf("want only key2, got %q", got)
	}
	if !reflect.DeepEqual(removed, []string{validTestKey1}) {
		t.Fatalf("removed = %v, want [key1]", removed)
	}
}

func TestReplaceAuthorizedKeys_EmptyKeepsPlatformKey(t *testing.T) {
	user, path := replaceFixture(t, validTestKey1+"\n"+validTestKey2+"\n\n"+sentinelBlock)
	removed, err := ReplaceAuthorizedKeys(user, nil, ReplaceHooks{})
	if err != nil {
		t.Fatal(err)
	}
	got := readFile(t, path)
	if strings.Contains(got, validTestKey1) || strings.Contains(got, validTestKey2) {
		t.Fatalf("caller keys survived: %q", got)
	}
	if !strings.Contains(got, sentinelBlock) {
		t.Fatalf("platform key lost: %q", got)
	}
	if len(removed) != 2 {
		t.Fatalf("removed = %v, want 2 caller keys (never the platform key)", removed)
	}
}

func TestReplaceAuthorizedKeys_Idempotent(t *testing.T) {
	user, path := replaceFixture(t, validTestKey3Seed()+"\n\n"+sentinelBlock)
	want := []string{validTestKey1, validTestKey2, validTestKey1} // duplicate collapses
	if _, err := ReplaceAuthorizedKeys(user, want, ReplaceHooks{}); err != nil {
		t.Fatal(err)
	}
	first := readFile(t, path)
	removed, err := ReplaceAuthorizedKeys(user, want, ReplaceHooks{})
	if err != nil {
		t.Fatal(err)
	}
	if second := readFile(t, path); second != first {
		t.Fatalf("second call changed file:\n%q\n%q", first, second)
	}
	if len(removed) != 0 {
		t.Fatalf("second call removed %v", removed)
	}
	if strings.Count(first, validTestKey1) != 1 {
		t.Fatalf("duplicate not collapsed: %q", first)
	}
}

func validTestKey3Seed() string { return replaceTestKey3 }

func TestReplaceAuthorizedKeys_InvalidKeyChangesNothing(t *testing.T) {
	orig := validTestKey1 + "\n\n" + sentinelBlock
	user, path := replaceFixture(t, orig)
	cases := map[string][]string{
		"malformed":  {validTestKey2, "ssh-ed25519 not-base64"},
		"options":    {validTestKey2, `command="/bin/sh" ` + validTestKey3Seed()},
		"from":       {`from="10.0.0.1" ` + validTestKey2},
		"multi-line": {validTestKey2 + "\n" + validTestKey3Seed()},
		"empty":      {validTestKey2, "   "},
	}
	for name, keys := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ReplaceAuthorizedKeys(user, keys, ReplaceHooks{}); err == nil {
				t.Fatal("want error")
			}
			if got := readFile(t, path); got != orig {
				t.Fatalf("file changed on rejected call: %q", got)
			}
		})
	}
}

func TestValidateStrictSSHPublicKey(t *testing.T) {
	if err := ValidateStrictSSHPublicKey(validTestKey1); err != nil {
		t.Fatalf("valid key rejected: %v", err)
	}
	for _, bad := range []string{
		`no-pty ` + validTestKey1,
		validTestKey1 + "\r\n" + validTestKey2,
		SentinelKeyMarker,
		"",
	} {
		if err := ValidateStrictSSHPublicKey(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func genKey(t *testing.T, comment string) string {
	t.Helper()
	return genKeyFrom(t, comment, func() ed25519.PublicKey {
		pub, _, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		return pub
	})
}

// genKeyFrom draws public keys from next until one passes the production
// validator. A random base64 body can contain a placeholder marker (e.g.
// "todo") and be rejected, so such draws are discarded, not returned. It
// asks the validator rather than keeping its own marker list.
func genKeyFrom(t *testing.T, comment string, next func() ed25519.PublicKey) string {
	t.Helper()
	for i := 0; i < 1000; i++ {
		pk, err := ssh.NewPublicKey(next())
		if err != nil {
			t.Fatal(err)
		}
		line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pk)))
		if comment != "" {
			line += " " + comment
		}
		if ValidateSSHPublicKey(line) == nil {
			return line
		}
	}
	t.Fatal("genKeyFrom: no validator-clean key after 1000 draws")
	return ""
}

// A draw whose encoding contains a placeholder marker must be regenerated.
func TestGenKey_RegeneratesPlaceholderMarkerDraw(t *testing.T) {
	// The wire blob has a 19-byte header (len+"ssh-ed25519"+len), so key
	// byte 2 starts a base64 group (offset 21 = 7*3). Put the 3 bytes that
	// encode "todo" there.
	marker, err := base64.StdEncoding.DecodeString("todo")
	if err != nil {
		t.Fatal(err)
	}
	bad := make(ed25519.PublicKey, ed25519.PublicKeySize)
	copy(bad[2:], marker)
	if ValidateSSHPublicKey(genKeyLineFor(t, bad)) == nil {
		t.Fatal("precondition: forced key should be rejected by the validator")
	}
	good, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	draws := 0
	got := genKeyFrom(t, "", func() ed25519.PublicKey {
		draws++
		if draws == 1 {
			return bad
		}
		return good
	})
	if draws < 2 {
		t.Fatalf("placeholder-marker draw was returned without regeneration (draws=%d)", draws)
	}
	if err := ValidateSSHPublicKey(got); err != nil {
		t.Fatalf("genKeyFrom returned a key the validator rejects: %v", err)
	}
}

func genKeyLineFor(t *testing.T, pub ed25519.PublicKey) string {
	t.Helper()
	pk, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pk)))
}

// A failing beforeWrite (registry revoke) leaves the file untouched, and a
// retry still sees the same keys to drop.
func TestReplaceAuthorizedKeys_HookFailureLeavesFileAndRetrySeesKeys(t *testing.T) {
	a, b := genKey(t, "a"), genKey(t, "b")
	orig := a + "\n" + b + "\n"
	user, path := replaceFixture(t, orig)

	_, err := ReplaceAuthorizedKeys(user, []string{b}, ReplaceHooks{BeforeWrite: func(removed []string) error {
		if len(removed) != 1 || removed[0] != a {
			t.Errorf("hook removed = %v", removed)
		}
		return fmt.Errorf("registry down")
	}})
	if err == nil {
		t.Fatal("hook error not returned")
	}
	if got := readFile(t, path); got != orig {
		t.Fatalf("file changed after failed hook: %q", got)
	}
	var seen []string
