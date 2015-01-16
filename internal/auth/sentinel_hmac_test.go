package auth

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

const testSecret = "abcdefghijklmnopqrstuvwxyz0123456789ABCD" // 40 bytes, satisfies SentinelMinSecretLen

func TestSignAndVerify_Roundtrip(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/authorized-keys", nil)
	SignSentinelRequest(req, []byte(testSecret))

	if err := VerifySentinelRequest(req, []byte(testSecret), time.Now()); err != nil {
		t.Fatalf("verify failed: %v", err)
	}
}

func TestVerify_MissingHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/certs", nil)
	if err := VerifySentinelRequest(req, []byte(testSecret), time.Now()); err == nil {
		t.Fatal("unsigned request must be rejected")
	}
}

func TestVerify_WrongSecret(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/certs", nil)
	SignSentinelRequest(req, []byte(testSecret))

	otherSecret := strings.Repeat("X", 40)
	if err := VerifySentinelRequest(req, []byte(otherSecret), time.Now()); err == nil {
		t.Fatal("request signed with a different secret must be rejected")
	}
}

func TestVerify_TamperedPath(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/certs", nil)
	SignSentinelRequest(req, []byte(testSecret))

	// Forge a different path on the same signature. The verifier
	// rebuilds the canonical string from req.URL.Path, so a path
	// change must invalidate the signature.
	tampered := httptest.NewRequest(http.MethodGet, "/authorized-keys", nil)
	tampered.Header.Set(SentinelHeaderTimestamp, req.Header.Get(SentinelHeaderTimestamp))
	tampered.Header.Set(SentinelHeaderSignature, req.Header.Get(SentinelHeaderSignature))

	if err := VerifySentinelRequest(tampered, []byte(testSecret), time.Now()); err == nil {
		t.Fatal("tampered path must invalidate signature")
	}
}

func TestVerify_TamperedMethod(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/certs", nil)
	SignSentinelRequest(req, []byte(testSecret))

	tampered := httptest.NewRequest(http.MethodPost, "/certs", nil)
	tampered.Header.Set(SentinelHeaderTimestamp, req.Header.Get(SentinelHeaderTimestamp))
	tampered.Header.Set(SentinelHeaderSignature, req.Header.Get(SentinelHeaderSignature))

	if err := VerifySentinelRequest(tampered, []byte(testSecret), time.Now()); err == nil {
		t.Fatal("tampered method must invalidate signature")
	}
}

func TestVerify_OldTimestampRejected(t *testing.T) {
