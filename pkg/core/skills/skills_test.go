package skills

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/footprintai/containarium/pkg/core/catalogsig"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

const extSkillYAML = `
skills:
  - id: ext-skill
    recipe_id: agent-runtime
    system_prompt: external
    allowed_scopes: [security:read]
`

// TestLoadDirVerified covers the optional provenance check (#648): a good
// signature loads, a missing or tampered one fails closed, and a nil verifier
// is the unchanged unsigned path.
func TestLoadDirVerified(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	v := catalogsig.NewVerifier(pub)

	writeCatalog := func(dir string, signWith ed25519.PrivateKey) {
		path := filepath.Join(dir, "ext.yaml")
		if err := os.WriteFile(path, []byte(extSkillYAML), 0o600); err != nil {
			t.Fatal(err)
		}
		if signWith != nil {
			sig := ed25519.Sign(signWith, []byte(extSkillYAML))
			if err := os.WriteFile(path+catalogsig.SigSuffix, []byte(base64.StdEncoding.EncodeToString(sig)), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}

	t.Run("good signature loads", func(t *testing.T) {
		dir := t.TempDir()
		writeCatalog(dir, priv)
		m := New()
		if err := m.LoadDirVerified(dir, v); err != nil {
			t.Fatalf("LoadDirVerified good sig: %v", err)
		}
		if _, err := m.Get("ext-skill"); err != nil {
			t.Error("signed external skill not merged")
		}
	})

	t.Run("missing signature fails", func(t *testing.T) {
		dir := t.TempDir()
		writeCatalog(dir, nil) // no .sig
		m := New()
		if err := m.LoadDirVerified(dir, v); err == nil {
			t.Fatal("expected unsigned file to fail in require-signed mode")
		}
		if _, err := m.Get("ext-skill"); err == nil {
			t.Error("unsigned skill must not be merged")
		}
	})

	t.Run("tampered payload fails", func(t *testing.T) {
		dir := t.TempDir()
		writeCatalog(dir, priv)
		// Mutate the catalog after signing.
		if err := os.WriteFile(filepath.Join(dir, "ext.yaml"), []byte(extSkillYAML+"\n# tampered\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		m := New()
		if err := m.LoadDirVerified(dir, v); err == nil {
			t.Fatal("expected tampered file to fail verification")
		}
	})

	t.Run("nil verifier loads unsigned", func(t *testing.T) {
		dir := t.TempDir()
		writeCatalog(dir, nil)
		m := New()
		if err := m.LoadDirVerified(dir, nil); err != nil {
			t.Fatalf("nil verifier should load unsigned: %v", err)
		}
		if _, err := m.Get("ext-skill"); err != nil {
			t.Error("nil-verifier path should merge unsigned skill")
		}
	})
