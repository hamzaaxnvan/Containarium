package mcp

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/url"
	"os"
	"strings"
)

// Phase 2.2 — MCP client TLS configuration (audit C-HIGH-1).
//
// The MCP client carries a Bearer JWT on every request. Without
// TLS, that token travels in cleartext and any passive listener on
// the daemon's network path harvests it. Without CA pinning, an
// attacker who can MITM the connection serves a self-signed cert
// for the same hostname and decrypts the traffic regardless.
//
// Two env vars control the policy:
//
//   CONTAINARIUM_MCP_ALLOW_INSECURE=true
//     Permits http:// baseURL. Default (unset) refuses it. Use
//     only for local dev / unit tests; production must be https.
//
//   CONTAINARIUM_MCP_TRUSTED_CA_FILE=/path/to/ca.crt
//     PEM bundle to use as the RootCAs pool. Use with the
//     sentinel-issued CA from Phase 0.5 (the /sentinel/ca
//     response) or a corporate root. Unset → fall back to the
//     system trust store, which is correct for publicly-trusted
