package sshauthz

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/sentinel/sshsession"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/tg123/sshpiper/libplugin"
	"golang.org/x/crypto/ssh"
)

// Bounds on the device-approval poll loop (#3084): it stops when the client
// drops, it runs for at most 3 logins' worth of connections per login and 10
// per source IP at once, an approval the client never answered is audited,
// and the approval URL's host is logged.

func challengeEnter(string, string, string, bool) (string, error) { return "", nil }

// sshpiperd only learns that a client is gone when the SSH connection is
// used, so each poll cycle sends a zero-prompt keyboard-interactive request
// (RFC 4256 lets the client answer it with no responses, and OpenSSH shows
// nothing for an empty name and instruction). A dropped client then fails
// in sshpiperd, which reports PipeCreateError for the client's address, and
// the poll for that address stops at once.
func TestDeviceApprovalPollStopsWhenClientDrops(t *testing.T) {
	f := newRouterFixture(t, "")
	wire := f.alice.PublicKey().Marshal()
	start := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	pending := pendingRes(start.Add(5*time.Minute), 5*time.Second)
	for _, tt := range []struct {
		name   string
		remote string // the address sshpiperd reports
		calls  int    // Authorize calls, the offer included
		gone   bool
	}{
		{name: "the client's own drop stops polling", remote: "203.0.113.1:1234", calls: 3, gone: true},
		{name: "another connection's failure does not", remote: "203.0.113.9:1", calls: 13},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newApprovalHarness(t, f, true, pending, []*pb.AuthorizeSSHResponse{pending}, nil)
			if h.cfg.PipeCreateErrorCallback == nil {
				t.Fatal("PipeCreateErrorCallback not registered: a dropped client is never seen")
			}
			h.sleepHook = func(n int) {
				if n == 2 {
					h.cfg.PipeCreateErrorCallback(tt.remote, errors.New("EOF"))
				}
			}
			conn := &libplugin.ConnMeta{UserName: "alice", FromAddr: "203.0.113.1:1234", UniqId: "conn-1"}
			if up, err := h.cfg.PublicKeyCallback(conn, wire); err != nil || up.GetRetryCurrentPlugin() == nil {
				t.Fatalf("pending: %v %v", up, err)
			}
			up, err := h.cfg.KeyboardInteractiveCallback(conn, challengeEnter)
			if err == nil || up != nil {
				t.Fatalf("must deny: %v %v", up, err)
			}
			if len(h.reqs) != tt.calls {
				t.Fatalf("Authorize calls = %d, want %d", len(h.reqs), tt.calls)
			}
			if tt.gone && !h.clock.Before(start.Add(MaxApprovalWait)) {
				t.Fatalf("polled until %v: the drop did not stop the loop", h.clock.Sub(start))
			}
			n := len(h.sink.recs)
			if n == 0 || h.sink.recs[n-1].Phase != sshsession.SessionPhaseDenied || h.sink.recs[n-1].DenyReason != pb.SSHDenyReason_SSH_DENY_REASON_APPROVAL_PENDING {
				t.Fatalf("denied record: %+v", h.sink.recs)
			}
			if logs := strings.Join(h.logs, "\n"); strings.Contains(logs, "client gone during device approval") != tt.gone {
				t.Fatalf("gone=%v, logs: %s", tt.gone, logs)
			}
			if m := h.methods(t, conn); m != "publickey" {
				t.Fatalf("methods after the outcome = %q", m)
			}
		})
	}
}

// Every poll cycle probes the client with a zero-prompt request and nothing
// else: the user still sees exactly one prompt and no extra text.
func TestDeviceApprovalProbesClientEachPoll(t *testing.T) {
	f := newRouterFixture(t, "")
	start := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	pending := pendingRes(start.Add(5*time.Minute), 5*time.Second)
	h := newApprovalHarness(t, f, true, pending, []*pb.AuthorizeSSHResponse{pending, pending, allowed("user-1")}, nil)
	conn := &libplugin.ConnMeta{UserName: "alice", FromAddr: "203.0.113.1:1234", UniqId: "conn-1"}
	if _, err := h.cfg.PublicKeyCallback(conn, f.alice.PublicKey().Marshal()); err != nil {
		t.Fatal(err)
	}
	var prompts, probes []string
	up, err := h.cfg.KeyboardInteractiveCallback(conn, func(user, instr, question string, echo bool) (string, error) {
		if question == "" {
			probes = append(probes, user+"|"+instr)
			return "", nil
		}
		prompts = append(prompts, question)
		return "", nil
	})
	if err != nil || up.GetOrGenerateUri() == "" {
		t.Fatalf("approval: %v %v", up, err)
	}
	// Polls at 0s, 5s, 10s: two waits, one probe after each, before the re-poll.
	if len(prompts) != 1 || len(probes) != 2 {
		t.Fatalf("prompts=%q probes=%q, want one prompt and a probe per wait", prompts, probes)
	}
	for _, p := range probes {
		if p != "|" {
			t.Fatalf("probe shows text %q: an empty name and instruction print nothing", p)
		}
	}
	t.Run("a broken stream on the probe stops polling", func(t *testing.T) {
		h := newApprovalHarness(t, f, true, pending, []*pb.AuthorizeSSHResponse{pending}, nil)
		if _, err := h.cfg.PublicKeyCallback(conn, f.alice.PublicKey().Marshal()); err != nil {
			t.Fatal(err)
		}
		up, err := h.cfg.KeyboardInteractiveCallback(conn, func(_, _, question string, _ bool) (string, error) {
			if question == "" {
				return "", errors.New("transport is closing")
			}
			return "", nil
		})
		if err == nil || up != nil || len(h.reqs) != 2 {
			t.Fatalf("must deny after the first poll: %v %v calls=%d", up, err, len(h.reqs))
		}
	})
}

// capHarness holds polls open: Sleep parks each poll until gate closes, so
// several connections are in the approval stage at once.
type capHarness struct {
	p        *Plugin
	cfg      libplugin.SshPiperPluginConfig
	mu       sync.Mutex
	logs     []string
	sink     *recordSink
	entered  chan struct{}
	gate     chan struct{}
	released atomic.Bool
}

func newCapHarness(t *testing.T, f routerFixture) *capHarness {
	t.Helper()
	h := &capHarness{sink: &recordSink{}, entered: make(chan struct{}, 64), gate: make(chan struct{})}
	start := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	pending := pendingRes(start.Add(5*time.Minute), 5*time.Second)
	p, err := New(Config{Enforce: true, Recorder: &lockedSink{h: h}, Now: func() time.Time { return start },
		Sleep: func(time.Duration) { h.entered <- struct{}{}; <-h.gate },
		Logf: func(format string, args ...any) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.logs = append(h.logs, fmt.Sprintf(format, args...))
		},
		Resolver: resolverFunc(func(context.Context, string) (string, bool, error) { return "backend-1", true, nil }),
		Authorizer: authorizerFunc(func(_ context.Context, _ string, r *pb.AuthorizeSSHRequest) (*pb.AuthorizeSSHResponse, error) {
			if r.GetApprovalId() == "" || !h.released.Load() {
				return pending, nil
			}
			return denyRes(pb.SSHDenyReason_SSH_DENY_REASON_APPROVAL_DENIED), nil
		}),
		Router: NewRouter(f.config), Target: sshsession.NewTargetResolver(f.config)})
	if err != nil {
		t.Fatal(err)
	}
	h.p, h.cfg = p, p.PluginConfig()
	return h
}

type lockedSink struct{ h *capHarness }

func (s *lockedSink) Record(r sshsession.Record) error {
	s.h.mu.Lock()
	defer s.h.mu.Unlock()
	s.h.sink.recs = append(s.h.sink.recs, r)
	return nil
}

// hold offers the key on conn and enters the approval stage in a goroutine,
// which then parks in the first poll wait.
func (h *capHarness) hold(t *testing.T, wire []byte, conn *libplugin.ConnMeta, wg *sync.WaitGroup) {
	t.Helper()
	if up, err := h.cfg.PublicKeyCallback(conn, wire); err != nil || up.GetRetryCurrentPlugin() == nil {
		t.Fatalf("%s: pending: %v %v", conn.UniqId, up, err)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = h.cfg.KeyboardInteractiveCallback(conn, challengeEnter)
	}()
	select {
	case <-h.entered:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s never reached the poll wait", conn.UniqId)
	}
}

