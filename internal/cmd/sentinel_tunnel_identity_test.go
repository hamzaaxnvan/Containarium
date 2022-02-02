//go:build !windows && !containarium_client

package cmd

import (
	"bytes"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/sentinel"
)

// captureLog redirects the standard logger for the duration of a test.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	return &buf
}

func TestConfigureTunnelTransport_FirstStartGeneratesThenRestartReuses(t *testing.T) {
	logs := captureLog(t)
	path := filepath.Join(t.TempDir(), "conf", "sentinel-tunnel-identity.pem")

	ts1 := sentinel.NewTunnelServer("", sentinel.NewTokenPolicy(), sentinel.NewTunnelRegistry(), 0)
	pin1, err := configureTunnelTransport(ts1, path)
	if err != nil {
		t.Fatalf("first start: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("identity file not created: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("identity file mode = %o, want 600", mode)
	}
	if !strings.Contains(logs.String(), string(pin1)) {
		t.Errorf("startup log does not contain the pin %s:\n%s", pin1, logs.String())
	}
	if !strings.Contains(logs.String(), "generated") {
		t.Errorf("first start should log that the identity was generated:\n%s", logs.String())
	}

	logs.Reset()
	ts2 := sentinel.NewTunnelServer("", sentinel.NewTokenPolicy(), sentinel.NewTunnelRegistry(), 0)
	pin2, err := configureTunnelTransport(ts2, path)
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	if pin2 != pin1 {
		t.Errorf("restart pin = %s, want reused %s", pin2, pin1)
	}
	if strings.Contains(logs.String(), "generated") {
		t.Errorf("restart must reuse, not regenerate:\n%s", logs.String())
	}
	if !strings.Contains(logs.String(), string(pin1)) {
		t.Errorf("restart log does not contain the pin:\n%s", logs.String())
	}
}
