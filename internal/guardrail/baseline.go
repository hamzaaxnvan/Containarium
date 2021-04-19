package guardrail

import (
	"regexp"
	"strings"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Baseline response policy (#2736, slice A of #2575). When no admin policy
// exists the model gateway scans responses against this table instead of
// scanning nothing. It is deliberately its own table, not the engine's SECRET
// rules: a pluggable engine must not widen or narrow what a default install
// checks. The accepted scope is PEM private-key headers and common cloud-key
// shapes only; widening it is a reviewed change to TestBaselineRules_StableIDs.

// BaselineRuleID is the stable id of one baseline rule, as audit records and
// counters name it.
type BaselineRuleID string

const (
	BaselineRulePEMPrivateKey   BaselineRuleID = "baseline.pem_private_key"
	BaselineRuleAWSAccessKeyID  BaselineRuleID = "baseline.aws_access_key_id"
	BaselineRuleGoogleAPIKey    BaselineRuleID = "baseline.google_api_key"
	BaselineRuleAzureStorageKey BaselineRuleID = "baseline.azure_storage_account_key"
)

// BaselineRule is one entry of the baseline table. literals is a prefilter:
// every match contains at least one of them, so a unit with none is skipped
// without running the pattern (a leading \b defeats the regexp package's own
// literal-prefix scan, which made a clean 8 MiB response cost ~0.3 s).
type BaselineRule struct {
	ID       BaselineRuleID
	Kind     pb.GuardrailKind
	pattern  *regexp.Regexp
	literals []string
}

var baselineRules = []BaselineRule{
	// RSA, EC, DSA, OPENSSH, ENCRYPTED, PGP ("... PRIVATE KEY BLOCK") or bare.
	{BaselineRulePEMPrivateKey, pb.GuardrailKind_GUARDRAIL_KIND_SECRET,
		regexp.MustCompile(`-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY(?: BLOCK)?-----`), []string{"PRIVATE KEY"}},
	// Long-term (AKIA) and temporary (ASIA) access key ids.
	{BaselineRuleAWSAccessKeyID, pb.GuardrailKind_GUARDRAIL_KIND_SECRET,
		regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`), []string{"AKIA", "ASIA"}},
	{BaselineRuleGoogleAPIKey, pb.GuardrailKind_GUARDRAIL_KIND_SECRET,
		regexp.MustCompile(`\bAIza[0-9A-Za-z_\-]{35}`), []string{"AIza"}},
	// Storage account key as it appears in a connection string.
	{BaselineRuleAzureStorageKey, pb.GuardrailKind_GUARDRAIL_KIND_SECRET,
		regexp.MustCompile(`AccountKey=[A-Za-z0-9+/]{86}==`), []string{"AccountKey="}},
}

func (r BaselineRule) mayMatch(text string) bool {
	for _, l := range r.literals {
		if strings.Contains(text, l) {
			return true
		}
	}
