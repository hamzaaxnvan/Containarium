package secrets

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Phase 4.1 Phase-C — AWS KMS backend tests.
//
// We stand up a fake KMS server with httptest that:
//   - INDEPENDENTLY re-derives the SigV4 signature from the
//     incoming request (a second implementation, written
//     inline below) and rejects a mismatch with a 403. This
//     is the real cross-check for the hand-rolled signer:
//     a round-trip only succeeds if production's canonical
//     request, string-to-sign, signing-key chain, and
//     scope all match an independent reimplementation.
//   - symmetric-encrypts the plaintext under a process-local
//     AES key to produce a base64 CiphertextBlob, reversing
//     on Decrypt.
//   - can be configured to return AWS-style errors.
//
// No real AWS credentials or network are needed.

type fakeAWSKMS struct {
	t                 *testing.T
	wantAccessKey     string
	wantSecret        string
	encryptKey        []byte
	statusCode        int    // override status (default 200)
	errType           string // AWS __type on forced error
	errMsg            string // AWS message on forced error
	lastSignedHeaders string // recorded from the last verified request
}

func newFakeAWSKMS(t *testing.T) (*httptest.Server, *fakeAWSKMS) {
	t.Helper()
	fk := &fakeAWSKMS{
		t:             t,
		wantAccessKey: "AKIDEXAMPLE",
		wantSecret:    "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
		encryptKey:    make([]byte, 32),
		statusCode:    200,
	}
	if _, err := io.ReadFull(rand.Reader, fk.encryptKey); err != nil {
		t.Fatalf("rand: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(fk.handle))
	return srv, fk
}

func (f *fakeAWSKMS) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)

	// Independent SigV4 verification. A signing bug in the
	// production code surfaces here as a 403.
	if err := f.verifySig(r, body); err != "" {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"__type":"InvalidSignatureException","message":"` + err + `"}`))
		return
	}

	if f.statusCode >= 400 {
		w.WriteHeader(f.statusCode)
		_, _ = w.Write([]byte(`{"__type":"` + f.errType + `","message":"` + f.errMsg + `"}`))
		return
	}

	target := r.Header.Get("X-Amz-Target")
	switch target {
	case "TrentService.Encrypt":
		var req struct {
			KeyId     string `json:"KeyId"`
			Plaintext string `json:"Plaintext"`
		}
		_ = json.Unmarshal(body, &req)
		pt, err := base64.StdEncoding.DecodeString(req.Plaintext)
		if err != nil {
			http.Error(w, `{"__type":"ValidationException","message":"bad plaintext b64"}`, http.StatusBadRequest)
			return
		}
		ct := f.symEncrypt(pt)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"KeyId":          "arn:aws:kms:us-west-2:111122223333:key/abcd",
			"CiphertextBlob": base64.StdEncoding.EncodeToString(ct),
		})
	case "TrentService.Decrypt":
		var req struct {
			KeyId          string `json:"KeyId"`
			CiphertextBlob string `json:"CiphertextBlob"`
		}
		_ = json.Unmarshal(body, &req)
		raw, err := base64.StdEncoding.DecodeString(req.CiphertextBlob)
		if err != nil {
			http.Error(w, `{"__type":"InvalidCiphertextException","message":"bad blob b64"}`, http.StatusBadRequest)
			return
		}
		pt, err := f.symDecrypt(raw)
