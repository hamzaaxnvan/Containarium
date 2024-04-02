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

