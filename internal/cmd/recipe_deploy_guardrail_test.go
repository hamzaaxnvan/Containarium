package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// TestRecipeGuardrailInput: --guardrail-staging-ref and
// --guardrail-attestation build guardrail_input; neither means an ungated
// deploy; one without the other is refused before any request is sent.
func TestRecipeGuardrailInput(t *testing.T) {
	att := &pb.GuardrailAttestation{SubjectSha256: "abc", KeyId: "k", Verdict: pb.GuardrailVerdict_GUARDRAIL_VERDICT_PASS}
	b, err := protojson.Marshal(att)
	if err != nil {
		t.Fatal(err)
	}
