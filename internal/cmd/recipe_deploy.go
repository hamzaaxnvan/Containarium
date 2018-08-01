package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

var (
	recipeDeployGPU       string
	recipeDeployBackendID string
	recipeDeployPool      string
	recipeDeployParams    []string

	recipeDeployGuardrailRef         string
	recipeDeployGuardrailAttestation string
)

// recipeGuardrailInput builds guardrail_input for a guardrail-gated recipe
// (#2368) from --guardrail-staging-ref and --guardrail-attestation. Neither
// set: nil (an ungated deploy; a gated recipe then refuses on the daemon).
func recipeGuardrailInput(ref, attestationFile string) (*pb.GuardrailGateInput, error) {
	if ref == "" && attestationFile == "" {
		return nil, nil
	}
