package sshsession

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tg123/sshpiper/libplugin"
)

// This fixture mirrors the exact shape internal/sentinel/keysync.go's
// renderSSHPiperConfig writes.
const testConfigYAML = `version: "1.0"
pipes:
  - from:
      - username: "boxuser"
        authorized_keys:
          - /etc/sshpiper/users/boxuser/authorized_keys
    to:
      host: 192.0.2.5:20022
      username: "boxuser"
      ignore_hostkey: true
      private_key: /etc/sshpiper/upstream_key
  - from:
      - username: "otheruser"
        authorized_keys:
          - /etc/sshpiper/users/otheruser/authorized_keys
        trusted_user_ca_keys:
          - /etc/sshpiper/trusted_user_ca_keys
    to:
      host: 192.0.2.9:22
      username: "otheruser"
      ignore_hostkey: true
      private_key: /etc/sshpiper/upstream_key
`

func TestTargetResolver_Resolve(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(testConfigYAML), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	r := NewTargetResolver(path)

	if got := r.Resolve("boxuser"); got != "192.0.2.5:20022" {
		t.Errorf("Resolve(boxuser) = %q, want 192.0.2.5:20022", got)
	}
	if got := r.Resolve("otheruser"); got != "192.0.2.9:22" {
		t.Errorf("Resolve(otheruser) = %q, want 192.0.2.9:22", got)
	}
	if got := r.Resolve("nosuchuser"); got != "" {
		t.Errorf("Resolve(nosuchuser) = %q, want empty", got)
	}
}

// A target stashed by the enforcing ssh-authz-plugin at routing time wins
// over a fresh config.yaml read, on the open and the close record alike;
