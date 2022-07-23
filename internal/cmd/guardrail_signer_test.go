package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/footprintai/containarium/internal/guardrail"
	"github.com/footprintai/containarium/internal/guardrailpolicy"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func keygenPrefix(t *testing.T) (prefix, keyID string) {
	t.Helper()
	prefix = filepath.Join(t.TempDir(), "k")
	if o, err := runGuardrail(t, "keygen", "--out", prefix); err != nil {
		t.Fatalf("keygen: %v\n%s", err, o)
	}
	pub, err := guardrail.LoadPublicKey(prefix + ".pub")
	must(t, err)
	return prefix, guardrail.KeyID(pub)
}

func TestGuardrailSigner_RotateRevokeBundle(t *testing.T) {
	fake := withFakeGuardrailPolicyAPI(t)
	oldP, oldID := keygenPrefix(t)
	newP, newID := keygenPrefix(t)
	setServerPolicy(t, fake, piiOnlyPolicy(), oldP)

	// not configured is an error, not a silent no-op
	t.Run("rotate adds the new key and keeps the old", func(t *testing.T) {
		o, err := runGuardrailAt(t, "signer", "rotate", "--add", newP+".pub", "--label", "2026-q4")
		if err != nil {
			t.Fatalf("rotate: %v\n%s", err, o)
