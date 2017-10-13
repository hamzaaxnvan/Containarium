package sshsession

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/tg123/sshpiper/libplugin"
	"golang.org/x/crypto/ssh"
)

// fakeRecorder collects every Record handed to it, in order.
type fakeRecorder struct {
	recs []Record
}

func (f *fakeRecorder) Record(rec Record) error {
	f.recs = append(f.recs, rec)
	return nil
}

// deferredMeta drives a publicKeyCallback result the same way sshpiperd's
// own ChainPlugins.onNextPlugin does: copy the returned NextPluginAuth's
// Meta into the ConnMeta that will be passed to the later PipeStart/
// PipeError calls. See internal/sentinel/sshsession/plugin.go's doc
// comment for why this same map is what makes credential propagation
// from auth-time to pipe-start-time work.
func deferredMeta(t *testing.T, upstream *libplugin.Upstream) map[string]string {
	t.Helper()
	next := upstream.GetNextPlugin()
	if next == nil {
		t.Fatalf("publicKeyCallback did not defer via NextPluginAuth: %+v", upstream)
	}
	return next.Meta
}

func TestPlugin_CertificateSession_OpenAndClose(t *testing.T) {
	// Acceptance criterion 1: an accepted certificate-auth SSH connection
	// emits one open and one close record, correlated by session_id.
	// Acceptance criterion 2: the record names the certificate's key_id
	// AND serial.
	ca := newTestCA(t)
	cert, _ := newTestUserCert(t, ca, "alice", 42)

	rec := &fakeRecorder{}
	p := &Plugin{Recorder: rec}

	conn := &libplugin.ConnMeta{
		UserName: "boxuser",
		FromAddr: "203.0.113.7:52341",
		UniqId:   "session-abc",
		Metadata: map[string]string{},
	}

	upstream, err := p.Config().PublicKeyCallback(conn, cert.Marshal())
	if err != nil {
		t.Fatalf("PublicKeyCallback: %v", err)
	}
	for k, v := range deferredMeta(t, upstream) {
		conn.Metadata[k] = v
	}

	cfg := p.Config()
	cfg.PipeStartCallback(conn)
	cfg.PipeErrorCallback(conn, nil)

	if len(rec.recs) != 2 {
		t.Fatalf("got %d records, want 2 (open + close): %+v", len(rec.recs), rec.recs)
	}

	open, closeRec := rec.recs[0], rec.recs[1]

	if open.Phase != SessionPhaseOpen {
		t.Errorf("first record phase = %q, want open", open.Phase)
	}
	if closeRec.Phase != SessionPhaseClose {
		t.Errorf("second record phase = %q, want close", closeRec.Phase)
	}
	if open.SessionID != "session-abc" || closeRec.SessionID != "session-abc" {
		t.Errorf("session ids not correlated: open=%q close=%q", open.SessionID, closeRec.SessionID)
	}
	if open.SessionID != closeRec.SessionID {
		t.Errorf("open/close session ids differ")
	}

	if open.AuthMethod != AuthMethodCertificate {
		t.Errorf("auth method = %q, want certificate", open.AuthMethod)
	}
	if open.KeyID != "alice" {
		t.Errorf("key id = %q, want alice", open.KeyID)
	}
	if open.Serial != 42 {
		t.Errorf("serial = %d, want 42", open.Serial)
	}
	if open.CAFingerprint == "" {
		t.Error("ca fingerprint should be populated for a certificate session")
	}

	if closeRec.CloseReason != CloseReasonNormal {
		t.Errorf("close reason = %q, want normal (nil pipe error)", closeRec.CloseReason)
	}
}

func TestPlugin_RawPublicKeySession_RecordsFingerprint(t *testing.T) {
	// Acceptance criterion 3: a raw-public-key connection records the key
	// fingerprint.
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pub, err := ssh.NewPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("build public key: %v", err)
	}

	rec := &fakeRecorder{}
	p := &Plugin{Recorder: rec}
	conn := &libplugin.ConnMeta{
		UserName: "boxuser",
		FromAddr: "203.0.113.8:9000",
		UniqId:   "session-raw",
		Metadata: map[string]string{},
	}

	upstream, err := p.Config().PublicKeyCallback(conn, pub.Marshal())
	if err != nil {
		t.Fatalf("PublicKeyCallback: %v", err)
	}
	for k, v := range deferredMeta(t, upstream) {
