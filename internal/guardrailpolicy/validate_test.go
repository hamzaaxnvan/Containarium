package guardrailpolicy_test

import (
	"crypto/ed25519"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/guardrail"
	"github.com/footprintai/containarium/internal/guardrailpolicy"
	"github.com/footprintai/containarium/internal/guardrailpolicy/storetest"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func TestValidate(t *testing.T) {
	good := storetest.Signer(t, 1, "release key")
	other := storetest.Signer(t, 2, "other key")
	mismatched := &pb.GuardrailTrustedSigner{KeyId: other.GetKeyId(), PublicKey: good.GetPublicKey(), Label: "mismatch"}
	// The short key's key_id is derived from the short key itself, so the
	// key_id check alone would accept it: only the length check refuses it.
	short := good.GetPublicKey()[:16]
	shortKey := &pb.GuardrailTrustedSigner{KeyId: guardrail.KeyID(ed25519.PublicKey(short)), PublicKey: short}
	// Likewise an empty key carrying the key_id of an empty key.
	emptyKey := &pb.GuardrailTrustedSigner{KeyId: guardrail.KeyID(nil)}

	rule := func(k pb.GuardrailKind, a pb.GuardrailAction, max int32) *pb.GuardrailRule {
		return &pb.GuardrailRule{Kind: k, Action: a, MaxResidual: max}
	}
	const (
		pii    = pb.GuardrailKind_GUARDRAIL_KIND_PII
		secret = pb.GuardrailKind_GUARDRAIL_KIND_SECRET
		redact = pb.GuardrailAction_GUARDRAIL_ACTION_REDACT
		block  = pb.GuardrailAction_GUARDRAIL_ACTION_BLOCK
	)

	cases := []struct {
		name    string
		policy  *pb.GuardrailPolicy
		signers []*pb.GuardrailTrustedSigner
		wantErr string // substring; empty = valid
	}{
		{name: "valid policy with signers", policy: storetest.Policy(1), signers: []*pb.GuardrailTrustedSigner{good, other}},
		{name: "empty rule list is valid", policy: &pb.GuardrailPolicy{}},
		{name: "missing policy", policy: nil, wantErr: "policy is required"},
		{name: "unspecified kind", policy: &pb.GuardrailPolicy{Rules: []*pb.GuardrailRule{rule(pb.GuardrailKind_GUARDRAIL_KIND_UNSPECIFIED, redact, 0)}}, wantErr: "kind"},
		{name: "unknown kind value", policy: &pb.GuardrailPolicy{Rules: []*pb.GuardrailRule{rule(pb.GuardrailKind(99), redact, 0)}}, wantErr: "kind"},
		{name: "unspecified action", policy: &pb.GuardrailPolicy{Rules: []*pb.GuardrailRule{rule(pii, pb.GuardrailAction_GUARDRAIL_ACTION_UNSPECIFIED, 0)}}, wantErr: "action"},
		{name: "unknown action value", policy: &pb.GuardrailPolicy{Rules: []*pb.GuardrailRule{rule(pii, pb.GuardrailAction(42), 0)}}, wantErr: "action"},
		{name: "duplicate kind", policy: &pb.GuardrailPolicy{Rules: []*pb.GuardrailRule{rule(pii, redact, 0), rule(secret, block, 0), rule(pii, block, 0)}}, wantErr: "duplicate"},
		{name: "negative residual", policy: &pb.GuardrailPolicy{Rules: []*pb.GuardrailRule{rule(pii, redact, -1)}}, wantErr: "max_residual"},
		{name: "nil rule", policy: &pb.GuardrailPolicy{Rules: []*pb.GuardrailRule{nil}}, wantErr: "kind"},
		{name: "signer key_id does not match its key", policy: storetest.Policy(0), signers: []*pb.GuardrailTrustedSigner{good, mismatched}, wantErr: "key_id"},
		// These wantErr strings are text only the length branch produces.
		{name: "signer key is not ed25519-sized", policy: storetest.Policy(0), signers: []*pb.GuardrailTrustedSigner{shortKey}, wantErr: "public_key is 16 bytes, want an Ed25519 key"},
		{name: "signer with an empty key", policy: storetest.Policy(0), signers: []*pb.GuardrailTrustedSigner{emptyKey}, wantErr: "public_key is 0 bytes, want an Ed25519 key"},
		{name: "nil signer", policy: storetest.Policy(0), signers: []*pb.GuardrailTrustedSigner{nil}, wantErr: "want an Ed25519 key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := guardrailpolicy.Validate(tc.policy, tc.signers)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate = %v, want an error mentioning %q", err, tc.wantErr)
			}
		})
