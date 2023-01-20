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
		if err != nil {
			http.Error(w, `{"__type":"InvalidCiphertextException","message":"bad ciphertext"}`, http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"KeyId":     "arn:aws:kms:us-west-2:111122223333:key/abcd",
			"Plaintext": base64.StdEncoding.EncodeToString(pt),
		})
	default:
		http.Error(w, `{"__type":"UnknownOperationException","message":"`+target+`"}`, http.StatusBadRequest)
	}
}

// verifySig re-derives the SigV4 signature from the request
// and compares it to the one in the Authorization header.
// Returns "" on success or a message on failure. This is a
// deliberately separate implementation from signV4 — they
// must agree for a round-trip to pass.
func (f *fakeAWSKMS) verifySig(r *http.Request, body []byte) string {
	authz := r.Header.Get("Authorization")
	const scheme = "AWS4-HMAC-SHA256 "
	if !strings.HasPrefix(authz, scheme) {
		return "missing AWS4-HMAC-SHA256 authorization"
	}
	var credential, signedHeaders, signature string
	for _, p := range strings.Split(strings.TrimPrefix(authz, scheme), ", ") {
		switch {
		case strings.HasPrefix(p, "Credential="):
			credential = strings.TrimPrefix(p, "Credential=")
		case strings.HasPrefix(p, "SignedHeaders="):
			signedHeaders = strings.TrimPrefix(p, "SignedHeaders=")
		case strings.HasPrefix(p, "Signature="):
			signature = strings.TrimPrefix(p, "Signature=")
		}
	}
	f.lastSignedHeaders = signedHeaders

	credParts := strings.Split(credential, "/")
	if len(credParts) != 5 {
		return "malformed credential scope"
	}
	access, dateStamp, region, service := credParts[0], credParts[1], credParts[2], credParts[3]
	if access != f.wantAccessKey {
		return "unknown access key id"
	}
	amzDate := r.Header.Get("X-Amz-Date")

	var ch strings.Builder
	for _, n := range strings.Split(signedHeaders, ";") {
		var val string
		if n == "host" {
			val = r.Host
		} else {
			val = r.Header.Get(n)
		}
		ch.WriteString(n)
		ch.WriteByte(':')
		ch.WriteString(strings.TrimSpace(val))
		ch.WriteByte('\n')
	}
	uri := r.URL.EscapedPath()
	if uri == "" {
		uri = "/"
	}
	ph := sha256.Sum256(body)
	canonicalRequest := strings.Join([]string{
		http.MethodPost, uri, "", ch.String(), signedHeaders, hex.EncodeToString(ph[:]),
	}, "\n")
	scope := strings.Join([]string{dateStamp, region, service, "aws4_request"}, "/")
	crHash := sha256.Sum256([]byte(canonicalRequest))
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256", amzDate, scope, hex.EncodeToString(crHash[:]),
	}, "\n")

	kDate := tHMAC([]byte("AWS4"+f.wantSecret), []byte(dateStamp))
	kRegion := tHMAC(kDate, []byte(region))
	kService := tHMAC(kRegion, []byte(service))
	kSigning := tHMAC(kService, []byte("aws4_request"))
	want := hex.EncodeToString(tHMAC(kSigning, []byte(stringToSign)))
	if want != signature {
		return "signature mismatch"
	}
	return ""
}

// tHMAC is the test's own HMAC-SHA256, kept separate from
// the production hmacSHA256 so the verifier shares no code
// with the signer beyond the Go standard library.
func tHMAC(key, data []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(data)
	return m.Sum(nil)
}

func (f *fakeAWSKMS) symEncrypt(pt []byte) []byte {
	block, _ := aes.NewCipher(f.encryptKey)
	gcm, _ := cipher.NewGCM(block)
	nonce := make([]byte, gcm.NonceSize())
	_, _ = io.ReadFull(rand.Reader, nonce)
	return append(nonce, gcm.Seal(nil, nonce, pt, nil)...)
}

func (f *fakeAWSKMS) symDecrypt(blob []byte) ([]byte, error) {
	block, _ := aes.NewCipher(f.encryptKey)
	gcm, _ := cipher.NewGCM(block)
	ns := gcm.NonceSize()
	if len(blob) < ns {
		return nil, io.ErrUnexpectedEOF
	}
	return gcm.Open(nil, blob[:ns], blob[ns:], nil)
}

const (
