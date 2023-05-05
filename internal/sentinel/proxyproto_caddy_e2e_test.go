//go:build proxyproto_real_caddy
// +build proxyproto_real_caddy

// This file is gated behind the proxyproto_real_caddy build tag because it
// shells out to a real `caddy` binary, which is heavy and not in the default
// CI image. Run it explicitly:
//
//	go test -tags=proxyproto_real_caddy -run TestProxyProtocolE2E_RealCaddy \
//	    -v ./internal/sentinel/...
//
// The test proves end-to-end that our PROXY v2 wire format is byte-compatible
// with Caddy's built-in `proxy_protocol` listener wrapper (Caddy 2.7+) — i.e.
// what the daemon actually runs in production. The companion test in
// proxyproto_e2e_test.go covers the same ground using pires/go-proxyproto as a
// stand-in parser; this one removes that one degree of separation by talking
// to a real Caddy.

package sentinel

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProxyProtocolE2E_RealCaddy(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real-Caddy e2e in short mode")
	}
	if runtime.GOOS == "darwin" {
		t.Skip("requires Linux loopback (binding 127.0.0.42 needs lo aliases on darwin)")
	}
	caddyBin, err := exec.LookPath("caddy")
	if err != nil {
		t.Skip("caddy binary not in PATH; install via `xcaddy build` and rerun")
	}

	// ---------------------------------------------------------------
	// 1. Backend: in-process HTTP server that echoes X-Forwarded-For
	// ---------------------------------------------------------------

	echoSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "x_forwarded_for=%s\n", r.Header.Get("X-Forwarded-For"))
		fmt.Fprintf(w, "remote_addr=%s\n", r.RemoteAddr)
	}))
	defer echoSrv.Close()
	echoPort := echoSrv.Listener.Addr().(*net.TCPAddr).Port

	// ---------------------------------------------------------------
	// 2. Generate a Caddy config with [proxy_protocol, tls] wrappers
	// ---------------------------------------------------------------

	caddyHTTPSPort := freePort(t)
	caddyAdminPort := freePort(t)

	storageDir := t.TempDir()
	cfg := fmt.Sprintf(`{
  "admin": {"listen": "127.0.0.1:%d"},
  "storage": {"module": "file_system", "root": %q},
  "apps": {
    "http": {
      "servers": {
        "srv0": {
          "listen": [":%d"],
          "automatic_https": {"disable_redirects": true},
          "listener_wrappers": [
            {"wrapper": "proxy_protocol", "timeout": "5s", "allow": ["127.0.0.0/8"]},
            {"wrapper": "tls"}
