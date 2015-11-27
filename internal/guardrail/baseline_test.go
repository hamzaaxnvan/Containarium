package guardrail

import (
	"reflect"
	"strings"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Every key below is synthetic: built from fixed filler, never a real key.
var (
	fakePEMHeader  = "-----BEGIN " + "RSA PRIVATE KEY-----"
	fakeAWSKeyID   = "AKIA" + "FAKEFAKEFAKEFAKE"
	fakeGoogleKey  = "AIza" + strings.Repeat("F", 35)
	fakeAzureKey   = "AccountKey=" + strings.Repeat("A", 86) + "=="
	fakeOpenSSHKey = "-----BEGIN " + "OPENSSH PRIVATE KEY-----"
)

func TestScanBaseline_Rules(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []BaselineDetection
	}{
		{"clean text", "nothing to see; -----BEGIN PUBLIC KEY----- is not secret", nil},
		{"pem rsa header", "here:\n" + fakePEMHeader + "\nMIIFAKE",
			[]BaselineDetection{{RuleID: BaselineRulePEMPrivateKey, Kind: pb.GuardrailKind_GUARDRAIL_KIND_SECRET, Matches: 1}}},
		{"pem openssh header", fakeOpenSSHKey,
			[]BaselineDetection{{RuleID: BaselineRulePEMPrivateKey, Kind: pb.GuardrailKind_GUARDRAIL_KIND_SECRET, Matches: 1}}},
		{"bare pem header", "-----BEGIN " + "PRIVATE KEY-----",
			[]BaselineDetection{{RuleID: BaselineRulePEMPrivateKey, Kind: pb.GuardrailKind_GUARDRAIL_KIND_SECRET, Matches: 1}}},
