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

