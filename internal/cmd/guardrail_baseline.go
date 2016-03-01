package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// `containarium guardrail baseline set --mode block|log` — the admin-only,
// audited switch for the baseline response scan (#2738, slice C of #2575).
// A thin client over GuardrailPolicyService.SetGuardrailBaselineMode
// (PUT /v1/guardrail/baseline).

var guardrailBaselineMode string

// guardrailBaselineAPI is the slice of the typed clients `baseline set` uses.
type guardrailBaselineAPI interface {
	SetGuardrailBaselineMode(mode pb.GuardrailBaselineMode) (*pb.SetGuardrailBaselineModeResponse, error)
}

// newGuardrailBaselineAPI builds the client from the global flags. A
// variable so tests can substitute a fake.
var newGuardrailBaselineAPI = func() (guardrailBaselineAPI, func(), error) {
	return dialGuardrailPolicyService()
}

// guardrailBaselineModes maps the --mode words onto the proto enum.
var guardrailBaselineModes = map[string]pb.GuardrailBaselineMode{
	"log":   pb.GuardrailBaselineMode_GUARDRAIL_BASELINE_MODE_LOG,
	"block": pb.GuardrailBaselineMode_GUARDRAIL_BASELINE_MODE_BLOCK,
}

var guardrailBaselineCmd = &cobra.Command{
	Use:   "baseline",
	Short: "Configure the baseline response scan (talks to the daemon)",
}

var guardrailBaselineSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Switch the baseline response scan between log and block (admin only)",
	Long: `Switch what the baseline does with a match. The baseline scans model
responses only while no admin guardrail policy has been set.

