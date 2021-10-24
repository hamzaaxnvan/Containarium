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

// Phase 4.1 Phase-C — GCP Cloud KMS backend tests.
//
// We stand up a fake Cloud KMS server with httptest that:
//   - validates the Authorization: Bearer header
//   - encrypts plaintext under a process-local AES key to
//     produce a base64 ciphertext blob
//   - reverses on the :decrypt verb
//   - can be configured to return errors for negative
//     cases
//
// This lets us exercise the request shape, the kek_id
// encoding, the Authorization plumbing, the error
// mapping, and the prefix-routing guard without needing
// real Cloud KMS credentials.

type fakeGCPKMS struct {
	t          *testing.T
	wantToken  string
	encryptKey []byte
	requireOp  string // if non-empty, fail unless URL ends with :<op>
	statusCode int    // override status code (default 200)
	errMsg     string // optional GCP-style error message
}

func newFakeGCPKMS(t *testing.T) (*httptest.Server, *fakeGCPKMS) {
	t.Helper()
	fk := &fakeGCPKMS{
		t:          t,
		wantToken:  "access-token-xyz",
		encryptKey: make([]byte, 32),
		statusCode: 200,
	}
	if _, err := io.ReadFull(rand.Reader, fk.encryptKey); err != nil {
		t.Fatalf("rand: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(fk.handle))
	return srv, fk
}

func (f *fakeGCPKMS) handle(w http.ResponseWriter, r *http.Request) {
	// Bearer token check up front so unauthenticated calls
	// fail the same way Cloud KMS would.
	authz := r.Header.Get("Authorization")
	if !strings.HasPrefix(authz, "Bearer ") || strings.TrimPrefix(authz, "Bearer ") != f.wantToken {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":401,"message":"missing or invalid token","status":"UNAUTHENTICATED"}}`))
		return
	}
	if f.statusCode >= 400 {
		w.WriteHeader(f.statusCode)
		_, _ = w.Write([]byte(`{"error":{"code":` +
			httpStatusJSON(f.statusCode) + `,"message":"` + f.errMsg + `"}}`))
		return
	}
	// Path: /v1/<key_name>:<op>
	// We don't validate the key_name; just extract op.
	path := strings.Trim(r.URL.Path, "/")
	idx := strings.LastIndex(path, ":")
	if idx < 0 {
		http.Error(w, `{"error":{"message":"missing :op suffix"}}`, http.StatusBadRequest)
		return
	}
	op := path[idx+1:]
	if f.requireOp != "" && op != f.requireOp {
		http.Error(w, `{"error":{"message":"wrong op"}}`, http.StatusBadRequest)
		return
	}
	body, _ := io.ReadAll(r.Body)
	switch op {
	case "encrypt":
		var req struct {
			Plaintext string `json:"plaintext"`
		}
		_ = json.Unmarshal(body, &req)
		pt, err := base64.StdEncoding.DecodeString(req.Plaintext)
		if err != nil {
			http.Error(w, `{"error":{"message":"bad plaintext b64"}}`, http.StatusBadRequest)
			return
		}
		ct := f.symEncrypt(pt)
		// As Cloud KMS does: `name` is the resource name of the
		// CryptoKeyVersion used (no API-version prefix).
		resp := map[string]any{
			"name":       strings.TrimPrefix(path[:idx], "v1/") + "/cryptoKeyVersions/1",
			"ciphertext": base64.StdEncoding.EncodeToString(ct),
		}
		_ = json.NewEncoder(w).Encode(resp)
	case "decrypt":
		var req struct {
			Ciphertext string `json:"ciphertext"`
		}
		_ = json.Unmarshal(body, &req)
		raw, err := base64.StdEncoding.DecodeString(req.Ciphertext)
		if err != nil {
			http.Error(w, `{"error":{"message":"bad ciphertext b64"}}`, http.StatusBadRequest)
			return
		}
		pt, err := f.symDecrypt(raw)
		if err != nil {
			http.Error(w, `{"error":{"message":"bad ciphertext"}}`, http.StatusBadRequest)
			return
		}
		resp := map[string]any{
			"plaintext": base64.StdEncoding.EncodeToString(pt),
		}
		_ = json.NewEncoder(w).Encode(resp)
	default:
