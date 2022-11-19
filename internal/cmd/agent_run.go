package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/footprintai/containarium/internal/agentengine"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/spf13/cobra"
)

var (
	agentRunBackendID         string
	agentRunPool              string
	agentRunInput             string
	agentRunGitSource         string
	agentRunGitRef            string
	agentRunGitCredentialFile string
	agentRunTrackerConnection string
	agentRunEngine            string
)

var agentRunCmd = &cobra.Command{
	Use:   "run <skill-id>",
	Short: "Run an agent skill in a box",
	Long: `Provision the skill's box, mint a token scoped to exactly the skill's
allowed_scopes, seed the system prompt + token + task input into the box, and
return the box.

Phase 0: the in-box agent loop (the agent-runtime image's job) consumes the
seed; the returned artifact is empty until that lands.

Examples:
  containarium agent run hello-agent --input '{"q":"hi"}' --server <host>
