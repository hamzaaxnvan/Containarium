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
}

func serveServer(box *serveBox) *AgentSkillServer {
	s := &AgentSkillServer{inBoxOps: box}
	// Short, so a box that never answers fails the test in milliseconds.
	s.serveReadyTimeout = 60 * time.Millisecond
	s.serveReadyInterval = 5 * time.Millisecond
	return s
}

func startServe(s *AgentSkillServer, ctx context.Context) error {
	return s.startServeMode(ctx, "agent-relay-agent-container", "/etc/containarium/agent/runs/run-1", "relay-agent", agentengine.Resolved{}, "")
}

// #2936: a healthy member still gets the old behaviour (kill any prior
// instance, then launch once) and is only reported started after its A2A
// endpoint answers.
func TestStartServeMode_HealthyMemberKillsThenLaunchesThenWaitsForTheAgentCard(t *testing.T) {
	box := &serveBox{probeFailures: 2}
	if err := startServe(serveServer(box), context.Background()); err != nil {
		t.Fatalf("a member whose endpoint answers on the third probe failed: %v", err)
	}
	if box.count("pkill") != 1 || box.count("setsid agent-runtime") != 1 {
		t.Errorf("kill=%d launch=%d, want exactly one of each (the pkill-then-relaunch behaviour is unchanged)", box.count("pkill"), box.count("setsid agent-runtime"))
	}
	if box.probes != 3 {
		t.Errorf("probes = %d, want 3 (two failures then the answer)", box.probes)
	}
	if !strings.HasPrefix(box.cmds[0], "bash -lc pkill") && !strings.Contains(box.cmds[0], "pkill") {
		t.Errorf("first command = %q, want the prior-instance kill", box.cmds[0])
	}
	if !strings.Contains(box.cmds[1], "setsid agent-runtime") {
		t.Errorf("second command = %q, want the launch", box.cmds[1])
	}
	if !strings.Contains(strings.Join(box.cmds, "\n"), "/dev/tcp/127.0.0.1/8674") {
		t.Error("the probe does not target the A2A port 8674 inside the box")
	}
}

// The launch itself failing is reported, not logged and dropped.
func TestStartServeMode_LaunchFailureIsReported(t *testing.T) {
	t.Run("exec error", func(t *testing.T) {
		box := &serveBox{launchErr: errors.New("instance not running")}
		err := startServe(serveServer(box), context.Background())
		if err == nil || !strings.Contains(err.Error(), "agent-relay-agent-container") || !strings.Contains(err.Error(), "instance not running") {
			t.Fatalf("got %v, want an error naming the box and the cause", err)
		}
		if errors.Is(err, errInBoxRuntimeMissing) {
			t.Error("a transport failure is not 'agent-runtime is not installed'")
		}
		if box.probes != 0 {
			t.Errorf("probed %d times after a failed launch", box.probes)
		}
	})
	t.Run("exit 127 is a missing runtime", func(t *testing.T) {
		box := &serveBox{launchExit: exitCommandNotFound}
		err := startServe(serveServer(box), context.Background())
		if !errors.Is(err, errInBoxRuntimeMissing) {
			t.Fatalf("got %v, want errInBoxRuntimeMissing", err)
		}
	})
	t.Run("incus 'Command not found' is a missing runtime", func(t *testing.T) {
		box := &serveBox{launchErr: errors.New("Command not found")}
		if err := startServe(serveServer(box), context.Background()); !errors.Is(err, errInBoxRuntimeMissing) {
			t.Fatalf("got %v, want errInBoxRuntimeMissing", err)
		}
	})
}
