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
