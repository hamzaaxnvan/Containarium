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
