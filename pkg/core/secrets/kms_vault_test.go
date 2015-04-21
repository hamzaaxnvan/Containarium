package secrets

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Phase 4.1 Phase-F — Vault Transit backend tests.
//
// We stand up a fake Vault Transit server with httptest
// that:
//   - validates the X-Vault-Token header
//   - encrypts plaintext under a process-local AES key
//     to produce a vault:v1:...-shaped blob
//   - reverses on /decrypt
//   - can be configured to return errors for negative
//     cases
//
// This lets us exercise the request shape, the kek_id
// encoding, the Authorization plumbing, the error
// mapping, and the prefix-routing guard without needing a
// real Vault.

type fakeVault struct {
	t          *testing.T
	wantToken  string
	encryptKey []byte
	requireOp  string // if non-empty, fail unless URL ends with this op
	statusCode int    // override status code (default 200)
	errMsg     string // optional Vault-style error to return when statusCode>=400
}

func newFakeVault(t *testing.T) (*httptest.Server, *fakeVault) {
	t.Helper()
	fv := &fakeVault{
		t:          t,
		wantToken:  "root-token",
		encryptKey: make([]byte, 32),
		statusCode: 200,
	}
	if _, err := io.ReadFull(rand.Reader, fv.encryptKey); err != nil {
		t.Fatalf("rand: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(fv.handle))
	return srv, fv
}

func (f *fakeVault) handle(w http.ResponseWriter, r *http.Request) {
	if got := r.Header.Get("X-Vault-Token"); got != f.wantToken {
		http.Error(w, `{"errors":["missing or invalid token"]}`, http.StatusForbidden)
		return
	}
	if f.statusCode >= 400 {
		w.WriteHeader(f.statusCode)
		_, _ = w.Write([]byte(`{"errors":["` + f.errMsg + `"]}`))
		return
	}
	// Path: /v1/transit/{op}/{key}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 4 || parts[0] != "v1" {
		http.Error(w, `{"errors":["bad path"]}`, http.StatusBadRequest)
		return
	}
	op := parts[2]
	if f.requireOp != "" && op != f.requireOp {
		http.Error(w, `{"errors":["wrong op"]}`, http.StatusBadRequest)
		return
	}
