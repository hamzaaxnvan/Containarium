//go:build !windows && !containarium_client

package cmd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/footprintai/containarium/internal/sentinel"
	cloudv1 "github.com/footprintai/containarium/pkg/pb/containarium/cloud/v1"
)

// The pin is public, so it goes on ExecStart (not the token env file); several
// candidate sentinels are carried as one comma-separated list.
func TestRenderTunnelUnit_SentinelPins(t *testing.T) {
	u := renderTunnelUnit(tunnelUnitParams{
		SentinelAddr: "s:443",
		SpotID:       "n",
		Ports:        "443",
		SentinelPins: []sentinel.TunnelPin{testPinA, testPinB},
	})
	want := "--sentinel-pin " + testPinA + "," + testPinB
	if !strings.Contains(u, want) {
		t.Errorf("tunnel unit missing %q\n%s", want, u)
	}
	if !strings.Contains(u[strings.Index(u, "ExecStart="):], want) {
		t.Errorf("pin must be in ExecStart:\n%s", u)
	}
}

func TestResolvePoolJoinPins(t *testing.T) {
	t.Setenv(sentinelPinEnv, "")
	if _, err := resolvePoolJoinPins(nil); err == nil || !strings.Contains(err.Error(), "--sentinel-pin") {
		t.Fatalf("no pin: err = %v, want a --sentinel-pin error", err)
	}
	if _, err := resolvePoolJoinPins([]string{"sha256:zz"}); err == nil || !strings.Contains(err.Error(), "invalid --sentinel-pin") {
		t.Fatalf("malformed pin: err = %v", err)
	}
	pins, err := resolvePoolJoinPins([]string{testPinA, testPinB + "," + testPinA})
	if err != nil {
		t.Fatal(err)
	}
	if len(pins) != 2 || pins[0] != testPinA || pins[1] != testPinB {
		t.Errorf("pins = %v, want [A B] deduplicated", pins)
	}
}

func TestRenderTunnelUnit_RequiredFlags(t *testing.T) {
	u := renderTunnelUnit(tunnelUnitParams{
		SentinelAddr: "sentinel.example.com:443",
		SpotID:       "node1",
		Ports:        "22,8080,443",
		Pool:         "prod",
	})
	for _, want := range []string{
		"--sentinel-addr sentinel.example.com:443",
		"--spot-id node1",
		"--ports 22,8080,443",
		"--pool prod",
		"WantedBy=multi-user.target",
		"Restart=always",
	} {
		if !strings.Contains(u, want) {
			t.Errorf("tunnel unit missing %q\n---\n%s", want, u)
		}
	}
	// No public hostname requested → no public flags rendered.
	if strings.Contains(u, "--public-hostname") || strings.Contains(u, "--public-port") {
		t.Errorf("tunnel unit should not carry public-* flags when unset:\n%s", u)
	}
}

// TestRenderTunnelUnit_TokenNeverInExecStart is the regression guard for
// #935: the tunnel-handshake token is a bearer-equivalent credential (the
// sentinel's TokenPolicy grants standing tunnel access to whoever presents
// it) and must never appear in the world-readable (0644) unit's ExecStart
// line — it's supplied via EnvironmentFile= instead (see
// tunnelTokenSecretFile), which `containarium tunnel` already reads as
// $CONTAINARIUM_TUNNEL_TOKEN.
func TestRenderTunnelUnit_TokenNeverInExecStart(t *testing.T) {
	u := renderTunnelUnit(tunnelUnitParams{
		SentinelAddr: "sentinel.example.com:443",
		SpotID:       "node1",
		Ports:        "22,8080,443",
		Pool:         "prod",
	})
	if strings.Contains(u, "--token") {
		t.Errorf("tunnel unit must not embed --token in ExecStart:\n%s", u)
	}
	if !strings.Contains(u, "EnvironmentFile="+tunnelTokenSecretFile) {
		t.Errorf("tunnel unit missing EnvironmentFile=%s:\n%s", tunnelTokenSecretFile, u)
	}
}

func TestRenderTunnelUnit_PublicPrimary(t *testing.T) {
	u := renderTunnelUnit(tunnelUnitParams{
		SentinelAddr:   "s:443",
		SpotID:         "n",
		Ports:          "443",
		Pool:           "prod",
		PublicHostname: "node1.example.com",
		PublicPort:     443,
	})
	if !strings.Contains(u, "--public-hostname node1.example.com") {
		t.Errorf("missing --public-hostname:\n%s", u)
	}
	if !strings.Contains(u, "--public-port 443") {
		t.Errorf("missing --public-port:\n%s", u)
	}
}

func TestRenderPoolDropIn(t *testing.T) {
	// ExecStart must be cleared then re-set (systemd override semantics).
	argv := resolvePoolDaemonArgv(nil, false, "prod", "", nil)
	d := renderPoolDropIn(argv, "")
	if !strings.Contains(d, "ExecStart=\nExecStart=/usr/local/bin/containariumd daemon") {
		t.Errorf("drop-in must clear+reset ExecStart:\n%s", d)
	}
	if !strings.Contains(d, "--pool prod") {
		t.Errorf("drop-in missing --pool:\n%s", d)
	}
	if strings.Contains(d, "--base-domain") {
		t.Errorf("drop-in should omit --base-domain when empty:\n%s", d)
	}
	if strings.Contains(d, "EnvironmentFile") {
		t.Errorf("drop-in should omit EnvironmentFile when no auth secret file given:\n%s", d)
	}
	d2 := renderPoolDropIn(resolvePoolDaemonArgv(nil, false, "prod", "boxes.example.com", nil), "")
	if !strings.Contains(d2, "--base-domain boxes.example.com") {
		t.Errorf("drop-in missing --base-domain when set:\n%s", d2)
	}
}

// TestRenderPoolDropIn_SentinelAuthSecret pins #687's fix: the drop-in must
// reference the auth-secret file via EnvironmentFile=, never embed the
// secret value inline (the drop-in itself stays 0644/world-readable).
func TestRenderPoolDropIn_SentinelAuthSecret(t *testing.T) {
	argv := resolvePoolDaemonArgv(nil, false, "prod", "", nil)
	d := renderPoolDropIn(argv, "/etc/containarium/sentinel-auth.env")
	if !strings.Contains(d, "EnvironmentFile=/etc/containarium/sentinel-auth.env") {
		t.Errorf("drop-in missing EnvironmentFile directive:\n%s", d)
	}
	if strings.Contains(d, "CONTAINARIUM_SENTINEL_AUTH_SECRET") {
		t.Errorf("drop-in must never embed the secret value inline:\n%s", d)
	}
}

// #959: pool join always warned --sentinel-auth-secret wasn't set for THIS
// invocation, even when the host already has it durably provisioned via an
// existing EnvironmentFile= drop-in (the documented, expected way — you
// provision it once, not on every join). These tests cover the detection
// helper directly.

func writeSecretFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
