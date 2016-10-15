package mcp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/footprintai/containarium/internal/connectcore"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// The in-box MCP `connect` tool runs INSIDE a box (e.g. the Alpine/musl
// managed-workspace LXC), where there is no system `ssh` binary to shell
// out to. So the exec/session paths use a pure-Go SSH client
// (golang.org/x/crypto/ssh) — the same approach internal/runner uses to
// provision boxes — making the mcp-server self-contained in any minimal
// image. Config mode still hands the human a ready `ssh …` line to run in
// their own terminal; only the non-interactive exec paths dial in-process.

const mcpSSHDialTimeout = 15 * time.Second

// withCertIfPresent wraps signer with the certificate issueCertForBox wrote
// next to the key as "<key>-cert.pub". OpenSSH picks that file up on its
// own; this pure-Go client does not, so without this the dial offered the
// bare throwaway key, which no authorized_keys holds, and every cert-auth
// connect failed with "no matching pipe" at the sentinel (#2179).
//
// No file = the managed-key path; signer is returned unchanged. A file that
// is present but unusable is an error rather than a quiet fallback to the
// bare key, which would only fail later with a far less useful message.
func withCertIfPresent(privPath string, signer ssh.Signer) (ssh.Signer, error) {
	certPath := privPath + "-cert.pub"
	certBytes, err := os.ReadFile(certPath) // #nosec G304 -- sibling of the key issueCertForBox wrote
	if errors.Is(err, os.ErrNotExist) {
		return signer, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read certificate %s: %w", certPath, err)
	}
	pub, _, _, _, err := ssh.ParseAuthorizedKey(certBytes)
	if err != nil {
		return nil, fmt.Errorf("parse certificate %s: %w", certPath, err)
	}
	cert, ok := pub.(*ssh.Certificate)
	if !ok {
		return nil, fmt.Errorf("%s holds a plain public key, not a certificate", certPath)
	}
	certSigner, err := ssh.NewCertSigner(cert, signer)
	if err != nil {
		return nil, fmt.Errorf("pair certificate with key: %w", err)
	}
	return certSigner, nil
}

// dialSSH opens a pure-Go SSH client to the target using the private key at
// privPath — and, when one sits next to it, the certificate issued for it.
// No system `ssh` binary required.
func dialSSH(ctx context.Context, t connectcore.Target, privPath string) (*ssh.Client, error) {
	keyBytes, err := os.ReadFile(privPath) // #nosec G304 -- privPath is the managed key sshkey.LocateOrGenerate resolved
	if err != nil {
		return nil, fmt.Errorf("read managed key %s: %w", privPath, err)
	}
	signer, err := ssh.ParsePrivateKey(keyBytes)
	if err != nil {
		return nil, fmt.Errorf("parse managed key: %w", err)
	}
	signer, err = withCertIfPresent(privPath, signer)
	if err != nil {
		return nil, err
	}
	hostKeyCB, err := tofuHostKeyCallback()
	if err != nil {
		return nil, err
	}
	cfg := &ssh.ClientConfig{
		User:            t.User,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: hostKeyCB,
		Timeout:         mcpSSHDialTimeout,
	}
	hostport := net.JoinHostPort(t.Host, strconv.Itoa(t.Port))
	d := &net.Dialer{Timeout: mcpSSHDialTimeout}
	conn, err := d.DialContext(ctx, "tcp", hostport)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", hostport, err)
	}
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, hostport, cfg)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ssh handshake %s: %w", hostport, err)
	}
	return ssh.NewClient(sshConn, chans, reqs), nil
}

// tofuHostKeyCallback returns an accept-new (trust-on-first-use) host-key
// callback backed by ~/.ssh/known_hosts — the pure-Go equivalent of ssh's
// StrictHostKeyChecking=accept-new the CLI uses: an unknown host is
// recorded and trusted, but a CHANGED key for a known host is rejected
// (MITM detection). This preserves the CLI's posture without shelling out.
func tofuHostKeyCallback() (ssh.HostKeyCallback, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = os.TempDir() // minimal container with no HOME — still get in-file TOFU
	}
	khPath := filepath.Join(home, ".ssh", "known_hosts")
	if err := os.MkdirAll(filepath.Dir(khPath), 0o700); err != nil {
		return nil, fmt.Errorf("create known_hosts dir: %w", err)
	}
	// knownhosts.New needs the file to exist; create it empty if absent.
	f, err := os.OpenFile(khPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) // #nosec G304 -- fixed ~/.ssh/known_hosts path
