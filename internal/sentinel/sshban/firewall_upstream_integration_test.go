//go:build integration && linux && sshfirewall

package sshban

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"golang.org/x/crypto/ssh"
)

// firewallFixture runs actual sshd and fail2ban only inside a fresh net namespace.
type firewallFixture struct {
	t                     *testing.T
	dir, conf, addr, port string
	bridgeStop            chan struct{}
	bridgeDone            chan struct{}
}

// TestIntegrationGuardedSSHDropFirewall proves TCP timeouts preserve jail budget.
func TestIntegrationGuardedSSHDropFirewall(t *testing.T) {
	if evidence := os.Getenv("SSH_UPSTREAM_EVIDENCE_DIR"); evidence != "" {
		t.Setenv("SSH_UPSTREAM_EVIDENCE_DIR", filepath.Join(evidence, "packet-drop"))
	}
	f := newFirewallFixture(t)
	controller, socket := guardedControl(t, f.addr, 10*time.Minute)
	main := newConfiguredRetryFixture(t, 100, 1, retryMode{routed: true, controlSocket: socket, backend: f.addr, backendUser: "root"})
	anon := newConfiguredRetryFixture(t, 100, 1, retryMode{routed: true, anonymous: true, controlSocket: socket, backend: f.addr, backendUser: "root"})
	f.write("authorized_keys", string(ssh.MarshalAuthorizedKey(main.upstream.PublicKey()))+string(ssh.MarshalAuthorizedKey(anon.upstream.PublicKey())))
	f.command("iptables", "-I", "INPUT", "1", "-s", "127.0.0.1", "-p", "tcp", "--dport", f.port, "-m", "conntrack", "--ctstate", "NEW", "-j", "DROP")
	started := time.Now()
	for time.Since(started) < 125*time.Second {
		for _, entry := range []*retryFixture{main, anon} {
			if err := retryHandshake(entry.addr, "127.0.0.2", entry.keys[0]); err == nil {
				t.Fatal("DROP outage authenticated")
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	attempts := len(main.attempts(t)) + len(anon.attempts(t))
	if attempts < 3 || attempts > 24 {
		t.Fatalf("DROP backoff stopped on false jail risk or stormed: %d dials", attempts)
	}
	f.command("iptables", "-D", "INPUT", "-s", "127.0.0.1", "-p", "tcp", "--dport", f.port, "-m", "conntrack", "--ctstate", "NEW", "-j", "DROP")
	if _, err := controller.HintSSHUpstreamRecovery(t.Context(), &pb.HintSSHUpstreamRecoveryRequest{BackendId: "backend"}); err != nil {
		t.Fatal(err)
	}
	restored := time.Now()
	for _, entry := range []*retryFixture{main, anon} {
		if err := retryHandshake(entry.addr, "127.0.0.2", entry.keys[0]); err != nil {
			t.Fatalf("pure TCP timeout retained false jail risk after repair: %v", err)
		}
		if entry.status(t, "127.0.0.2").Status.GetFailures() != 0 {
			t.Fatal("TCP timeout charged source failure")
		}
	}
	if time.Since(restored) > 3*time.Second {
		t.Fatal("DROP recovery exceeded three seconds with safe budget")
	}
	if strings.Contains(f.jail("get", "sshd", "banip"), "127.0.0.1") {
		t.Fatal("DROP outage banned sentinel")
	}
	f.write("drop-status.log", f.jail("status", "sshd"))
	t.Logf("DROP outage=%s backend_connects=%d; no jail ban; safe-budget recovery=%s", time.Since(started), attempts, time.Since(restored))
}

// firewallCommand runs fixed fixture tools with bounded execution and real output.
func (f *firewallFixture) command(name string, args ...string) string {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(f.t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("%s: %v\n%s", name, err, out)
	}
	return string(out)
}

// write stores only the selected fixture file in its private directory.
func (f *firewallFixture) write(name, body string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, name), []byte(body), 0600); err != nil {
		f.t.Fatal(err)
	}
}

// start reaps the actual server process on success and every failed assertion.
func (f *firewallFixture) start(name string, args ...string) {
	f.t.Helper()
	log, err := os.Create(filepath.Join(f.dir, filepath.Base(name)+".process.log"))
	if err != nil {
		f.t.Fatal(err)
	}
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		_ = log.Close()
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait(); _ = log.Close() })
}

// newFirewallFixture refuses host-network execution before invoking any firewall tool.
func newFirewallFixture(t *testing.T) *firewallFixture {
	t.Helper()
	self, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		t.Fatal(err)
	}
	host, err := os.Readlink("/proc/1/ns/net")
	if err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() != 0 || self == host || os.Getenv("SSH_UPSTREAM_ISOLATED_NETNS") != "1" {
		t.Fatal("run with sudo unshare --net; host firewall execution is forbidden")
	}
	f := &firewallFixture{t: t, dir: t.TempDir(), addr: retryUnusedAddress(t), bridgeStop: make(chan struct{}), bridgeDone: make(chan struct{})}
	// Activate connection tracking before creating baseline flows. Installing
	// the first conntrack rule only after a handshake misclassifies that
	// previously untracked flow as NEW and invalidates session-survival proof.
	f.command("iptables", "-A", "INPUT", "-m", "conntrack", "--ctstate", "ESTABLISHED,RELATED", "-j", "ACCEPT")
