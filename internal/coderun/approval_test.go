package coderun

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestParseApprovalMode(t *testing.T) {
	for in, want := range map[string]ApprovalMode{"": ApprovalsDeny, "deny": ApprovalsDeny, "ask": ApprovalsAsk, " ask ": ApprovalsAsk} {
		got, err := ParseApprovalMode(in)
		if err != nil || got != want {
			t.Errorf("ParseApprovalMode(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := ParseApprovalMode("allow"); err == nil {
		t.Error("ParseApprovalMode(\"allow\") accepted a value that is not a mode")
	}
}

// An answer verifies only under the public key of the private key that signed
// it, and only for the run, request, tool call and answer it was signed for:
// no replay onto another request or run, no turning a deny into an allow, and
// no answer made for a tool call the box did not ask (#2184: the journal a
// human reads is writable by the agent, so what it shows can be a forgery).
func TestVerifyAnswerBindsKeyRunRequestToolCallAndAnswer(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	otherPub, otherPriv, _ := ed25519.GenerateKey(rand.Reader)
	shown := RequestDigest("Bash", `{"command":"rm -rf ~/repo"}`)
	good := FormatAnswer(priv, "code", "req-1", shown, AnswerAllow)

	if a, err := VerifyAnswer(pub, "code", "req-1", shown, good); err != nil || a != AnswerAllow {
		t.Fatalf("VerifyAnswer(good) = %q, %v; want allow", a, err)
	}
	denySig := SignAnswer(priv, "code", "req-1", shown, AnswerDeny)
	cases := map[string]struct {
		pub     ed25519.PublicKey
		run, id string
		digest  []byte
		content string
	}{
		"other public key":  {otherPub, "code", "req-1", shown, good},
		"signed by another": {pub, "code", "req-1", shown, FormatAnswer(otherPriv, "code", "req-1", shown, AnswerAllow)},
		"other request":     {pub, "code", "req-2", shown, good},
		"other run":         {pub, "other", "req-1", shown, good},
		"other input":       {pub, "code", "req-1", RequestDigest("Bash", `{"command":"ls"}`), good},
		"other tool":        {pub, "code", "req-1", RequestDigest("Write", `{"command":"rm -rf ~/repo"}`), good},
		"no request digest": {pub, "code", "req-1", nil, good},
		"deny sig as allow": {pub, "code", "req-1", shown, "allow " + denySig},
		"unsigned":          {pub, "code", "req-1", shown, "allow"},
		"not hex":           {pub, "code", "req-1", shown, "allow zz"},
		"not an answer":     {pub, "code", "req-1", shown, "maybe " + SignAnswer(priv, "code", "req-1", shown, "maybe")},
		"extra field":       {pub, "code", "req-1", shown, strings.TrimSpace(good) + " extra"},
		"empty":             {pub, "code", "req-1", shown, ""},
	}
	for name, c := range cases {
		if a, err := VerifyAnswer(c.pub, c.run, c.id, c.digest, c.content); err == nil {
			t.Errorf("%s: VerifyAnswer accepted %q as %q", name, c.content, a)
		}
	}
}

func TestParseApprovalPublicKey(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	if got, err := ParseApprovalPublicKey(hex.EncodeToString(pub)); err != nil || !got.Equal(pub) {
		t.Errorf("round trip = %x, %v", got, err)
	}
	for _, bad := range []string{"", "zz", hex.EncodeToString(pub[:16])} {
		if _, err := ParseApprovalPublicKey(bad); err == nil {
			t.Errorf("ParseApprovalPublicKey(%q) accepted", bad)
		}
	}
}

func TestValidApprovalRunName(t *testing.T) {
	for _, ok := range []string{"code", "sess-42", "a_b.c", "..x"} {
		if err := ValidApprovalRunName(ok); err != nil {
			t.Errorf("ValidApprovalRunName(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", ".", "..", "sub/task", "../victim", "a b", "x\x00"} {
		if err := ValidApprovalRunName(bad); err == nil {
			t.Errorf("ValidApprovalRunName(%q) accepted", bad)
		}
