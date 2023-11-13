package alert

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/coderun/engine"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

type fileCredentialProber struct{ home string }

func (p fileCredentialProber) CredentialWatchTargets(context.Context) ([]CredentialWatchTarget, error) {
	return []CredentialWatchTarget{{SkillID: "s1", Box: "agent-s1-container", Engine: pb.AgentEngine_AGENT_ENGINE_CLAUDE}}, nil
}
func (p fileCredentialProber) ProbeCredential(ctx context.Context, _ CredentialWatchTarget) (pb.CodeCredentialSource, error) {
	script, _ := engine.CredentialAlertStatusScript(engine.NameClaude)
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", script)
	cmd.Env = []string{"HOME=" + p.home, "PATH=" + os.Getenv("PATH")}
	out, err := cmd.Output()
	if err != nil {
		return srcNone, err
	}
	switch strings.TrimSpace(string(out)) {
