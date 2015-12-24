package sshban

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/sentinel/sshcontrol"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/tg123/sshpiper/libplugin"
	"google.golang.org/protobuf/types/known/durationpb"
)

// TestSourcePolicyUsesTypedBackendProvenance keeps server outages out of IP bans.
func TestSourcePolicyUsesTypedBackendProvenance(t *testing.T) {
	dir, err := os.MkdirTemp("", "sshsource-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	now := time.Now()
	p := &pb.SSHUpstreamPolicy{InitialDelay: durationpb.New(time.Second), MaxDelay: durationpb.New(8 * time.Second), Window: durationpb.New(time.Minute), SetupTimeout: durationpb.New(5 * time.Second), FailureBudget: 2, RiskPerAttempt: 1, MaxBackends: 8, MaxPending: 8, MaxConnections: 32, EgressGroup: "fixture", Routes: []*pb.SSHUpstreamRoute{{BackendId: "backend", Endpoint: "127.0.0.1:22"}}}
	c, err := sshcontrol.New(sshcontrol.Config{Policy: p, StatePath: filepath.Join(dir, "risk.json"), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	t.Cleanup(func() { _ = c.Close() })
	stop, err := sshcontrol.ListenControl(filepath.Join(dir, "control.sock"), c)
