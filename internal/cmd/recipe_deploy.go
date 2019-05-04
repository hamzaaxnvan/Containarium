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
	if ref == "" || attestationFile == "" {
		return nil, fmt.Errorf("--guardrail-staging-ref and --guardrail-attestation go together")
	}
	b, err := os.ReadFile(attestationFile) // #nosec G304 -- operator-supplied --guardrail-attestation path
	if err != nil {
		return nil, err
	}
	att := &pb.GuardrailAttestation{}
	if err := protojson.Unmarshal(b, att); err != nil {
		return nil, fmt.Errorf("--guardrail-attestation %s: %w", attestationFile, err)
	}
	return &pb.GuardrailGateInput{StagingRef: ref, Attestation: att}, nil
}

var recipeDeployCmd = &cobra.Command{
	Use:   "deploy <recipe-id> <name>",
	Short: "Deploy a recipe as a new dedicated container",
	Long: `Provision a new dedicated container from a recipe, run the recipe's
image inside it, and expose its ports.

In v1 the recipe deploys on the backend that --server points at. To deploy on
a GPU node, point --server at that node's daemon and pass --gpu.

Examples:
  containarium recipe deploy ollama ol1 --gpu 0 --param model=llama3 --server <host>
  containarium recipe deploy llamacpp lc1 --gpu 0 --param hf_repo=ggml-org/gemma-3-1b-it-GGUF --server <host>`,
	Args: cobra.ExactArgs(2),
	RunE: runRecipeDeploy,
}

func init() {
