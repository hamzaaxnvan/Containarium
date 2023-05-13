package recipes

import (
	"strings"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func gatedYAML(gate string) string {
	return "recipes:\n  - id: gated\n    image: x\n    guardrail_gate:\n" + gate
}

// TestLoadGuardrailGate: a recipe's guardrail_gate reaches the proto as
// typed enums, and a gate the deploy could not honour is refused at load.
func TestLoadGuardrailGate(t *testing.T) {
	m := New()
	if err := m.LoadFromBytes([]byte(gatedYAML("      dataset_path: /data/train\n      require_kinds: [secret, pii]\n"))); err != nil {
		t.Fatalf("load: %v", err)
	}
	r, err := m.Get("gated")
	if err != nil {
		t.Fatal(err)
	}
	g := r.GetGuardrailGate()
	want := []pb.GuardrailKind{pb.GuardrailKind_GUARDRAIL_KIND_SECRET, pb.GuardrailKind_GUARDRAIL_KIND_PII}
