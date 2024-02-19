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
