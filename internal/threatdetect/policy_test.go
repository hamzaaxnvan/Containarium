package threatdetect

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/footprintai/containarium/internal/app"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// fakeConfigStore stands in for *app.DaemonConfigStore: the same Get/Set
// contract, including the ErrDaemonConfigNotFound sentinel for a missing
// key (see daemon_config_store.go — absence is an error, not "").
type fakeConfigStore struct {
	values map[string]string
	err    error
}

func (f *fakeConfigStore) Get(_ context.Context, key string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	v, ok := f.values[key]
	if !ok {
		return "", app.ErrDaemonConfigNotFound
	}
	return v, nil
}

func (f *fakeConfigStore) Set(_ context.Context, key, value string) error {
	if f.err != nil {
		return f.err
	}
	if f.values == nil {
		f.values = map[string]string{}
	}
	f.values[key] = value
	return nil
}

func policy(action pb.ProcessAction, allowance string, ttl time.Duration) *pb.ProcessPolicy {
	p := &pb.ProcessPolicy{Action: action, ThrottleAllowance: allowance}
	if ttl != 0 {
		p.Ttl = durationpb.New(ttl)
	}
	return p
}

// effective is policy() as ResolveProcessPolicy returns it: a resolved
// policy always carries kill_process, false unless a level set it (#2638).
func effective(action pb.ProcessAction, allowance string, ttl time.Duration) *pb.ProcessPolicy {
	p := policy(action, allowance, ttl)
	p.KillProcess = proto.Bool(false)
	return p
}

// The effective-policy resolution table from the design doc: container
// override → backend default → built-in {ALERT, "10%", 24h}, field by field
// so a partial override inherits what it leaves empty.
func TestResolveProcessPolicy(t *testing.T) {
	tests := []struct {
		name              string
		override, backend *pb.ProcessPolicy
		want              *pb.ProcessPolicy
	}{
		{
			name: "nothing stored is the built-in ALERT",
			want: effective(pb.ProcessAction_PROCESS_ACTION_ALERT, "10%", 24*time.Hour),
		},
		{
			name:    "backend default applies when no override",
			backend: policy(pb.ProcessAction_PROCESS_ACTION_THROTTLE, "5%", 2*time.Hour),
			want:    effective(pb.ProcessAction_PROCESS_ACTION_THROTTLE, "5%", 2*time.Hour),
		},
		{
			name:     "override wins over backend default",
			override: policy(pb.ProcessAction_PROCESS_ACTION_FREEZE, "", 0),
			backend:  policy(pb.ProcessAction_PROCESS_ACTION_THROTTLE, "5%", 2*time.Hour),
			want:     effective(pb.ProcessAction_PROCESS_ACTION_FREEZE, "5%", 2*time.Hour),
		},
		{
			name:    "partial backend default inherits built-in fields",
			backend: policy(pb.ProcessAction_PROCESS_ACTION_THROTTLE, "", 0),
			want:    effective(pb.ProcessAction_PROCESS_ACTION_THROTTLE, "10%", 24*time.Hour),
		},
		{
			name:     "override can set ALERT explicitly over a THROTTLE default",
			override: policy(pb.ProcessAction_PROCESS_ACTION_ALERT, "", 0),
			backend:  policy(pb.ProcessAction_PROCESS_ACTION_THROTTLE, "", 0),
			want:     effective(pb.ProcessAction_PROCESS_ACTION_ALERT, "10%", 24*time.Hour),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveProcessPolicy(tt.override, tt.backend)
			if !proto.Equal(got, tt.want) {
				t.Fatalf("ResolveProcessPolicy() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidateProcessPolicy(t *testing.T) {
	tests := []struct {
		name    string
		p       *pb.ProcessPolicy
		wantErr string
	}{
		{name: "nil is an error", p: nil, wantErr: "policy is required"},
		{name: "empty inherits everything", p: &pb.ProcessPolicy{}},
		{name: "throttle with share allowance", p: policy(pb.ProcessAction_PROCESS_ACTION_THROTTLE, "10%", time.Hour)},
		{name: "throttle with hard quota", p: policy(pb.ProcessAction_PROCESS_ACTION_THROTTLE, "10ms/100ms", 0)},
		{name: "unknown action", p: &pb.ProcessPolicy{Action: pb.ProcessAction(42)}, wantErr: "action"},
		{name: "allowance without unit", p: policy(pb.ProcessAction_PROCESS_ACTION_THROTTLE, "10", 0), wantErr: "throttle_allowance"},
		{name: "allowance over 100%", p: policy(pb.ProcessAction_PROCESS_ACTION_THROTTLE, "150%", 0), wantErr: "throttle_allowance"},
		{name: "allowance zero", p: policy(pb.ProcessAction_PROCESS_ACTION_THROTTLE, "0%", 0), wantErr: "throttle_allowance"},
		{name: "negative ttl", p: policy(pb.ProcessAction_PROCESS_ACTION_ALERT, "", -time.Minute), wantErr: "ttl"},
		{name: "ttl under a minute", p: policy(pb.ProcessAction_PROCESS_ACTION_ALERT, "", 30*time.Second), wantErr: "ttl"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateProcessPolicy(tt.p)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateProcessPolicy() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ValidateProcessPolicy() = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

// The store persists protojson under the two documented DaemonConfigStore
// keys, treats an empty value as "unset" (that is how ClearOverride works —
// DaemonConfigStore has no delete), and resolves Effective per container.
func TestProcessPolicyStore_RoundTrip(t *testing.T) {
	ctx := context.Background()
	cfg := &fakeConfigStore{}
	s := NewProcessPolicyStore(cfg)

	if got, err := s.Default(ctx); err != nil || got != nil {
		t.Fatalf("Default() on empty store = %v, %v; want nil, nil", got, err)
	}
	eff, err := s.Effective(ctx, "box-a")
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	if !proto.Equal(eff, BuiltinProcessPolicy()) {
		t.Fatalf("Effective() on empty store = %v, want built-in", eff)
	}

	def := policy(pb.ProcessAction_PROCESS_ACTION_THROTTLE, "5%", time.Hour)
	if err := s.SetDefault(ctx, def); err != nil {
		t.Fatalf("SetDefault: %v", err)
	}
	raw, ok := cfg.values[ProcessPolicyKey]
	if !ok {
