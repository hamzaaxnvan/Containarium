package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/agentengine"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// serveBox scripts the box a serve-mode launch talks to. It tells the four
// commands startServeMode runs apart by what they contain: the prior-instance
// kill, the launch, the /agent-card probe and the log tail.
type serveBox struct {
	mu sync.Mutex

	launchExit int
	launchErr  error
	// probeFailures is how many probes fail before one succeeds; -1 = never.
	probeFailures int
	logTail       string

	cmds   []string
	probes int
}

func (b *serveBox) ExecWithExitCode(_ string, cmd []string) (string, string, int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	joined := strings.Join(cmd, " ")
	b.cmds = append(b.cmds, joined)
	switch {
	case strings.Contains(joined, "pkill"):
		return "", "", 1, nil // nothing matched: benign
	case strings.Contains(joined, "setsid agent-runtime"):
		return "", "", b.launchExit, b.launchErr
	case strings.Contains(joined, "/agent-card"):
		b.probes++
		if b.probeFailures >= 0 && b.probes > b.probeFailures {
			return "", "", 0, nil
		}
		return "", "", 1, nil
	case strings.Contains(joined, "tail -c"):
		return b.logTail, "", 0, nil
	}
	return "", "", 0, nil
}

func (b *serveBox) ReadFile(_, _ string) ([]byte, error) { return nil, errors.New("not used") }

func (b *serveBox) count(substr string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, c := range b.cmds {
		if strings.Contains(c, substr) {
			n++
		}
	}
	return n
