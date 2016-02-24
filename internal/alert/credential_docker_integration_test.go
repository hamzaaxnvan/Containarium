//go:build integration

package alert

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/coderun/engine"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

type dockerCredentialProber struct {
	name    string
	failAck bool
}

func (p *dockerCredentialProber) run(ctx context.Context, args ...string) ([]byte, error) {
	command := append([]string{"exec", "-e", "HOME=/guest", p.name}, args...)
	return exec.CommandContext(ctx, "docker", command...).Output()
}
func (p *dockerCredentialProber) CredentialWatchTargets(context.Context) ([]CredentialWatchTarget, error) {
	return oneTargetProber(srcInteractive).targets, nil
}
func (p *dockerCredentialProber) ProbeCredential(ctx context.Context, _ CredentialWatchTarget) (pb.CodeCredentialSource, error) {
	script, _ := engine.CredentialAlertStatusScript(engine.NameClaude)
	out, err := p.run(ctx, "sh", "-c", script)
	if err != nil {
		return srcNone, err
	}
	switch strings.TrimSpace(string(out)) {
	case "interactive":
		return srcInteractive, nil
	case "expired":
		return srcExpired, nil
	case "api-key":
		return pb.CodeCredentialSource_CODE_CREDENTIAL_SOURCE_API_KEY, nil
	case "none":
		return srcNone, nil
	}
	return srcNone, fmt.Errorf("invalid probe name")
}
func (p *dockerCredentialProber) CredentialEpisodes(ctx context.Context, _ CredentialWatchTarget) (CredentialEpisodes, error) {
	out, err := p.run(ctx, "node", "/tmp/app/dist/credential-state-cli.js", "snapshot")
	var result CredentialEpisodes
	if err != nil {
		return result, err
	}
	err = json.Unmarshal(out, &result)
	return result, err
}
func (p *dockerCredentialProber) AcknowledgeCredentialEpisodes(ctx context.Context, _ CredentialWatchTarget, ids []string) error {
	if p.failAck {
		return fmt.Errorf("synthetic acknowledgement failure")
	}
	args := append([]string{"node", "/tmp/app/dist/credential-state-cli.js", "ack"}, ids...)
	_, err := p.run(ctx, args...)
	return err
}

func TestCredentialDockerEndToEnd(t *testing.T) {
	if os.Getenv("CONTAINARIUM_CREDENTIAL_DOCKER_E2E") != "1" {
		t.Skip("explicit opt-in required for isolated Docker fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	bundle, err := filepath.Abs("../../bin/agent-runtime-bundle.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	deps, err := filepath.Abs("../../agent-runtime/node_modules")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	name := fmt.Sprintf("containarium-2790-%d", time.Now().UnixNano())
	image := os.Getenv("CONTAINARIUM_CREDENTIAL_DOCKER_IMAGE")
	if image == "" {
		image = "node:20-bookworm-slim"
