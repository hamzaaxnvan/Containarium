package mcp

// Tests for certificate-based `connect` (cloud #1083).
//
// The fallback is the path that matters most today: a plain Containarium
// daemon has no signing endpoint, so every existing deployment takes it. A
// regression there breaks `connect` for everyone, while a regression on the
// certificate path breaks it only where certificates are available. The tests
// weight accordingly.

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// certStubAPI is an API whose doRequest is scripted per path, so a test can
// say "the signing endpoint 404s" or "it returns this body" without a daemon.
type certStubAPI struct {
	API
	responses map[string][]byte
	errs      map[string]error
	// calls records every path hit, so a test can assert that the managed-key
	// authorize endpoint was NOT called on the certificate path.
	calls []string
}

func (s *certStubAPI) doRequest(method, path string, _ interface{}) ([]byte, error) {
	s.calls = append(s.calls, method+" "+path)
	if err, ok := s.errs[path]; ok {
		return nil, err
	}
	if body, ok := s.responses[path]; ok {
		return body, nil
	}
	return nil, errors.New("unexpected path " + path)
}

func (s *certStubAPI) called(substr string) bool {
	for _, c := range s.calls {
		if strings.Contains(c, substr) {
			return true
		}
	}
	return false
}

func TestIssueCertForBox_FallsBackWhenServerCannotSign(t *testing.T) {
	// Every error shape a daemon without the endpoint can produce.
	for _, msg := range []string{
		"POST /v1/ssh/certificates:issue: HTTP 404: Not Found",
		"HTTP 501: Not Implemented",
		"rpc error: code = Unimplemented desc = unknown method",
	} {
		t.Run(msg, func(t *testing.T) {
			api := &certStubAPI{errs: map[string]error{certIssuePath: errors.New(msg)}}
			_, err := issueCertForBox(api, "cld-abc")
			if !errors.Is(err, errCertUnsupported) {
				t.Fatalf("issueCertForBox() = %v, want errCertUnsupported so connect falls back", err)
			}
		})
	}
}

// The other half of the fallback contract: a server that CAN sign and fails
// must not be papered over. Falling back would install a long-lived key to
// work around a control-plane fault and never mention it.
func TestIssueCertForBox_RealFailuresAreNotSwallowed(t *testing.T) {
	for _, msg := range []string{
		"HTTP 403: permission denied",
		"HTTP 500: issue certificate failed",
		"HTTP 401: invalid or expired token",
		"dial tcp: connection refused",
	} {
		t.Run(msg, func(t *testing.T) {
			api := &certStubAPI{errs: map[string]error{certIssuePath: errors.New(msg)}}
			_, err := issueCertForBox(api, "cld-abc")
			if err == nil {
				t.Fatal("expected an error")
			}
			if errors.Is(err, errCertUnsupported) {
				t.Fatalf("a real failure (%q) was misread as 'server cannot sign' — connect would silently install a long-lived key", msg)
			}
		})
	}
}

// A 200 carrying no certificate is a server that claimed to sign and produced
// nothing. That must be an error, not a fallback.
func TestIssueCertForBox_EmptyCertificateIsAnError(t *testing.T) {
	api := &certStubAPI{responses: map[string][]byte{
		certIssuePath: []byte(`{"certificate":"","principals":[]}`),
	}}
	_, err := issueCertForBox(api, "cld-abc")
	if err == nil {
		t.Fatal("an empty certificate must be an error")
	}
