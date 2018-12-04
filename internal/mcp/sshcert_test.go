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
	if errors.Is(err, errCertUnsupported) {
		t.Fatal("an empty certificate must not read as 'server cannot sign'")
	}
}

func TestIssueCertForBox_WritesAUsableIdentity(t *testing.T) {
	// Sign a real certificate so the on-disk layout is exercised end to end.
	caSigner, _ := newCertTestCA(t)
	var captured struct {
		PublicKey    string   `json:"public_key"`
		ContainerIDs []string `json:"container_ids"`
	}

	api := &recordingCertAPI{
		certStubAPI: certStubAPI{},
		sign: func(body interface{}) ([]byte, error) {
			raw, _ := json.Marshal(body)
			_ = json.Unmarshal(raw, &captured)

			pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(captured.PublicKey))
			if err != nil {
				return nil, err
			}
			cert := &ssh.Certificate{
				Key:             pub,
				CertType:        ssh.UserCert,
				KeyId:           "user=test",
				ValidPrincipals: []string{"cld-abc"},
				ValidAfter:      0,
				ValidBefore:     ssh.CertTimeInfinity,
			}
			if err := cert.SignCert(cryptoRand(), caSigner); err != nil {
				return nil, err
			}
			return json.Marshal(map[string]any{
				"certificate": string(ssh.MarshalAuthorizedKey(cert)),
				"principals":  []string{"cld-abc"},
				"validBefore": time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339),
			})
		},
	}

	got, err := issueCertForBox(api, "cld-abc")
	if err != nil {
		t.Fatalf("issueCertForBox() error = %v", err)
	}
	defer got.Cleanup()

	// Scoped to the one box. An empty list would ask for every container the
	// token can reach — on an org-wide token, a certificate for the whole org.
	if len(captured.ContainerIDs) != 1 || captured.ContainerIDs[0] != "cld-abc" {
		t.Errorf("container_ids = %v, want exactly [cld-abc]", captured.ContainerIDs)
	}

	fi, err := os.Stat(got.PrivateKeyPath)
	if err != nil {
		t.Fatalf("private key not written: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("private key mode = %o, want 0600 (ssh refuses looser)", perm)
	}
	// OpenSSH only presents a certificate that sits next to its key as
	// "<key>-cert.pub"; anywhere else and it is silently ignored.
	certPath := got.PrivateKeyPath + "-cert.pub"
	raw, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("certificate not written next to the key: %v", err)
	}
	parsed, _, _, _, err := ssh.ParseAuthorizedKey(raw)
	if err != nil {
		t.Fatalf("written certificate does not parse: %v", err)
	}
	if _, ok := parsed.(*ssh.Certificate); !ok {
		t.Fatalf("written file is %T, not a certificate", parsed)
	}

	// The identity must not outlive the call — that is the whole property.
	got.Cleanup()
	if _, err := os.Stat(got.PrivateKeyPath); !os.IsNotExist(err) {
		t.Error("ephemeral key survived Cleanup — a certificate login that leaves its key behind gives up what it exists for")
	}
}

func TestCertTTLNote(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	if got := certTTLNote(&issuedCert{ValidBefore: now.Add(5 * time.Minute)}, now); !strings.Contains(got, "5m0s") {
		t.Errorf("certTTLNote = %q, want the remaining lifetime", got)
	}
	// An absent expiry must still read sensibly rather than as a huge or
	// negative duration.
	if got := certTTLNote(&issuedCert{}, now); !strings.Contains(got, "short-lived") {
		t.Errorf("certTTLNote with no expiry = %q", got)
	}
}

// --- helpers ---

// recordingCertAPI lets a test sign the exact key the code under test
// generated, so the certificate is real rather than a fixture string.
type recordingCertAPI struct {
	certStubAPI
	sign func(body interface{}) ([]byte, error)
}

func (r *recordingCertAPI) doRequest(method, path string, body interface{}) ([]byte, error) {
