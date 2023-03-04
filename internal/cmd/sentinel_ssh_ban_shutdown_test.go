//go:build !windows && !containarium_client

package cmd

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/sentinel/sshban"
	"github.com/footprintai/containarium/internal/sentinel/sshsession"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

type shutdownSink struct {
	mu      sync.Mutex
	records []sshsession.Record
	closed  bool
	entered chan struct{}
	release chan struct{}
}

func (s *shutdownSink) Record(r sshsession.Record) error {
	if s.entered != nil {
		close(s.entered)
		<-s.release
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("write after close")
	}
	s.records = append(s.records, r)
	return nil
}

func (s *shutdownSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func (s *shutdownSink) snapshot() (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.records), s.closed
}

func shutdownGuard(t *testing.T, now *time.Time, emit func(sshban.Event)) *sshban.Guard {
	t.Helper()
	g, err := sshban.New(sshban.Config{
		Listener: pb.SSHBanListener_SSH_BAN_LISTENER_MAIN, MaxFailures: 3,
		Duration: time.Minute, MaxEntries: 10, Now: func() time.Time { return *now }, Emit: emit,
	})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func startShutdownRecorder(a *sshban.AuditRecorder, sink *shutdownSink, ticks <-chan time.Time, g *sshban.Guard) (context.CancelFunc, <-chan struct{}) {
	ctx, cancel := context.WithCancel(context.Background())
	reopenDone := make(chan struct{})
	close(reopenDone)
	return cancel, startSSHBanAudit(ctx, a, ticks, g, func() {}, reopenDone, sink)
}

func shutdownDeadline(t *testing.T, stop func(), g *sshban.Guard, a *sshban.AuditRecorder, cancel context.CancelFunc, done <-chan struct{}) {
	t.Helper()
	ctx, end := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer end()
	returned := make(chan error, 1)
	go func() { returned <- shutdownSSHBanAudit(ctx, stop, g, a, cancel, done) }()
	select {
	case err := <-returned:
		if !errors.Is(err, context.DeadlineExceeded) {
