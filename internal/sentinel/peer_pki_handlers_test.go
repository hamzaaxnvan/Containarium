package sentinel

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/pkg/core/pki"
)

// End-to-end coverage for Phase 0.5: the daemon-side bootstrap path
// (FetchPeerPKI in internal/server) is symmetric to these handlers,
// so a roundtrip test here is the cleanest way to guard against
// drift. We exercise:
//
//   - GET /sentinel/ca returns the PEM bundle when CA is wired
//   - GET /sentinel/ca returns 503 when CA is unwired
//   - POST /sentinel/peer-cert mints a verifiable cert
//   - both handlers reject requests without a valid HMAC

const phase05Secret = "abcdefghijklmnopqrstuvwxyz0123456789ABCD" // 40 bytes

func newManagerForPKITest(t *testing.T, withCA bool) *Manager {
	t.Helper()
	m := &Manager{backends: NewBackendPool()}
	m.SetHMACSecret([]byte(phase05Secret))
	if withCA {
		caKeyPEM, err := pki.GenerateCAKey()
		if err != nil {
			t.Fatalf("GenerateCAKey: %v", err)
		}
		prov, err := pki.NewFromKey(caKeyPEM, 0)
		if err != nil {
