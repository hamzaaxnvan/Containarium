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
		{"aws access key id twice", "id " + fakeAWSKeyID + " and " + fakeAWSKeyID,
			[]BaselineDetection{{RuleID: BaselineRuleAWSAccessKeyID, Kind: pb.GuardrailKind_GUARDRAIL_KIND_SECRET, Matches: 2}}},
		{"google api key", "key=" + fakeGoogleKey,
			[]BaselineDetection{{RuleID: BaselineRuleGoogleAPIKey, Kind: pb.GuardrailKind_GUARDRAIL_KIND_SECRET, Matches: 1}}},
		{"azure storage key", "DefaultEndpointsProtocol=https;" + fakeAzureKey,
			[]BaselineDetection{{RuleID: BaselineRuleAzureStorageKey, Kind: pb.GuardrailKind_GUARDRAIL_KIND_SECRET, Matches: 1}}},
		{"two rules, table order", fakeAWSKeyID + " " + fakePEMHeader,
			[]BaselineDetection{
				{RuleID: BaselineRulePEMPrivateKey, Kind: pb.GuardrailKind_GUARDRAIL_KIND_SECRET, Matches: 1},
				{RuleID: BaselineRuleAWSAccessKeyID, Kind: pb.GuardrailKind_GUARDRAIL_KIND_SECRET, Matches: 1},
			}},
		// Out of the baseline on purpose (Q4: PEM headers and cloud-key shapes only).
		{"pii is not baseline", "mail a@example.com, card 4111 1111 1111 1111", nil},
		{"short AKIA is not a key id", "AKIA123", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ScanBaseline([]*pb.GuardrailTextUnit{{UnitId: "u", Text: tc.text}})
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ScanBaseline = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// Matches are summed per rule across units, so one response gives one
// detection per rule however its text was split.
func TestScanBaseline_SumsAcrossUnits(t *testing.T) {
	got := ScanBaseline([]*pb.GuardrailTextUnit{
		{UnitId: "text/0", Text: fakeAWSKeyID},
		{UnitId: "tool/0/0", Text: `{"k":"` + fakeAWSKeyID + `"}`},
	})
