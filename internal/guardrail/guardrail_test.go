package guardrail

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

var testKey = []byte("0123456789abcdef0123456789abcdef")

func pii(typ string, start, end int32) *pb.GuardrailFinding {
	return &pb.GuardrailFinding{UnitId: "u", Start: start, End: end, Kind: pb.GuardrailKind_GUARDRAIL_KIND_PII, Type: typ, Confidence: 1}
}

// The reference engine reports CODE POINT offsets. A multi-byte prefix
// before the match is the case a byte-offset bug would get wrong.
func TestRulesEngine_OffsetsAreCodePoints(t *testing.T) {
	text := "客戶來電：mail is ann@example.com, ssn 123-45-6789."
	resp, err := RulesEngine{}.Scan(context.Background(), &pb.GuardrailScanRequest{
		Units: []*pb.GuardrailTextUnit{{UnitId: "u", Text: text}},
	})
	if err != nil {
		t.Fatal(err)
	}
	runes := []rune(text)
	got := map[string]string{}
	for _, f := range resp.Findings {
		got[f.Type] = string(runes[f.Start:f.End])
	}
	if got["EMAIL"] != "ann@example.com" {
		t.Errorf("EMAIL slice = %q", got["EMAIL"])
	}
	if got["SSN"] != "123-45-6789" {
		t.Errorf("SSN slice = %q", got["SSN"])
	}
	if resp.EngineId != RulesEngineID || !strings.HasPrefix(resp.EngineVersion, "rules@") {
		t.Errorf("engine identity = %q %q", resp.EngineId, resp.EngineVersion)
	}
}

func TestRulesEngine_KindFilterAndLuhn(t *testing.T) {
	text := "card 4111 1111 1111 1111 and not-a-card 1234 5678 9012 3456; key AKIAIOSFODNN7EXAMPLE"
	resp, err := RulesEngine{}.Scan(context.Background(), &pb.GuardrailScanRequest{
		Units: []*pb.GuardrailTextUnit{{UnitId: "u", Text: text}},
		Kinds: []pb.GuardrailKind{pb.GuardrailKind_GUARDRAIL_KIND_PII},
	})
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, f := range resp.Findings {
		types = append(types, f.Type)
	}
	if strings.Join(types, ",") != "CREDIT_CARD" {
		t.Errorf("PII-only scan found %v; want exactly the Luhn-valid card, no secret", types)
	}
	if _, err := (RulesEngine{}).Scan(context.Background(), &pb.GuardrailScanRequest{Kinds: []pb.GuardrailKind{pb.GuardrailKind_GUARDRAIL_KIND_UNSPECIFIED}}); err == nil {
		t.Error("UNSPECIFIED kind must be rejected, not treated as 'all'")
	}
}

func TestRedact_StableTokensAndVault(t *testing.T) {
	units := []*pb.GuardrailTextUnit{
		{UnitId: "u", Text: "a@x.io wrote to a@x.io; key -----BEGIN PRIVATE KEY-----"},
		{UnitId: "clean", Text: "nothing here"},
	}
	findings := []*pb.GuardrailFinding{
		pii("EMAIL", 0, 6), pii("EMAIL", 16, 22),
		{UnitId: "u", Start: 28, End: 55, Kind: pb.GuardrailKind_GUARDRAIL_KIND_SECRET, Type: "PRIVATE_KEY", Confidence: 1},
	}
	res, err := Redact(units, findings, DefaultPolicy(), testKey)
	if err != nil {
		t.Fatal(err)
	}
	tok := Token(testKey, pb.GuardrailKind_GUARDRAIL_KIND_PII, "EMAIL", "a@x.io")
	if want := tok + " wrote to " + tok + "; key " + Token(testKey, pb.GuardrailKind_GUARDRAIL_KIND_SECRET, "PRIVATE_KEY", "-----BEGIN PRIVATE KEY-----"); res.Wiped["u"] != want {
		t.Errorf("wiped = %q\nwant    %q", res.Wiped["u"], want)
