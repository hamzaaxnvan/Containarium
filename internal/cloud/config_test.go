package cloud

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestConfigRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "cloud.yaml")
	in := &Config{ControlPlane: "cloud.example:443", HostID: "11111111-1111-1111-1111-111111111111", Token: "secret-bearer", JWTSecretFile: "/etc/containarium/jwt.secret"}
	if err := Save(path, in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if out == nil || *out != *in {
		t.Fatalf("round-trip mismatch: got %+v want %+v", out, in)
	}
}

func TestLoadMissingIsNotEnrolled(t *testing.T) {
	got, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil {
		t.Fatalf("missing file must not error: %v", err)
	}
	if got != nil {
		t.Fatalf("missing file should yield nil config (not enrolled), got %+v", got)
	}
}

func TestSaveIs0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permissions")
	}
	path := filepath.Join(t.TempDir(), "cloud.yaml")
	if err := Save(path, &Config{ControlPlane: "x:443", HostID: "h", Token: "t"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
