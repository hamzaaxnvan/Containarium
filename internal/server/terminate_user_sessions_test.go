package server

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/cloud"
	"github.com/footprintai/containarium/internal/collaborator"
	"github.com/footprintai/containarium/internal/sshauthz"
	"github.com/footprintai/containarium/pkg/core/container"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// sessionRecorder is a fake Incus exec plus a fake host pkill writing one
// ordered command log, so a test can assert the exact sequence across the
// box and the host (#2522). Every other incus.Backend method panics (nil
// embed), so a stray delete or file write fails the test loudly.
type sessionRecorder struct {
	incus.Backend
	mu      sync.Mutex
	boxes   map[string]string // container name -> state
	log     []string
	tenants map[string]string // container name -> incus.TenantLabelKey value (optional)
	killed  map[string]bool   // account -> pkill matched (box and host)
	collabs []*collaborator.Collaborator
	listErr error
}

func (r *sessionRecorder) GetContainer(name string) (*incus.ContainerInfo, error) {
	state, ok := r.boxes[name]
	if !ok {
		return nil, errors.New("not found")
	}
	return &incus.ContainerInfo{Name: name, State: state, Tenant: r.tenants[name]}, nil
}

func (r *sessionRecorder) Exec(name string, cmd []string) error {
	r.record("box " + name + ": " + strings.Join(cmd, " "))
	return nil
}

func (r *sessionRecorder) ExecWithExitCode(name string, cmd []string) (string, string, int, error) {
	r.record("box " + name + ": " + strings.Join(cmd, " "))
	if r.killed[cmd[len(cmd)-1]] {
		return "", "", 0, nil
	}
	return "", "", 1, nil
}

func (r *sessionRecorder) hostKill(account string) bool {
	r.record("host: pkill -KILL -u " + account)
	return r.killed[account]
}

func (r *sessionRecorder) ListByCollaborator(_ context.Context, user string) ([]*collaborator.Collaborator, error) {
	var out []*collaborator.Collaborator
	for _, c := range r.collabs {
		if c.CollaboratorUsername == user {
			out = append(out, c)
		}
	}
	return out, r.listErr
}

func (r *sessionRecorder) record(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.log = append(r.log, s)
}

func newSessionServer(r *sessionRecorder) *ContainerServer {
	return &ContainerServer{
		manager:         container.NewWithBackend(r),
		collabAccess:    r,
		hostSessionKill: r.hostKill,
	}
}

func collab(owner, user string) *collaborator.Collaborator {
	return &collaborator.Collaborator{
		ContainerName:        owner + "-container",
		OwnerUsername:        owner,
		CollaboratorUsername: user,
		AccountName:          owner + "-container-" + user,
	}
}

func TestEndUserSessions_Walk(t *testing.T) {
	cases := []struct {
		name           string
		rec            *sessionRecorder
		want           []string
		wantTerminated int32
		wantBoxes      int32
	}{
		{
			name: "owner box only",
			rec: &sessionRecorder{
				boxes:  map[string]string{"alice-container": "Running", "bob-container": "Running"},
				killed: map[string]bool{"alice": true},
			},
			want: []string{
				"box alice-container: loginctl terminate-user alice",
				"box alice-container: pkill -KILL -u alice",
				"host: pkill -KILL -u alice",
			},
			// alice had processes in her box and on her jump account.
			wantTerminated: 2, wantBoxes: 1,
		},
		{
			name: "collaborator on two boxes",
			rec: &sessionRecorder{
				boxes: map[string]string{"bob-container": "Running", "carol-container": "Running"},
				collabs: []*collaborator.Collaborator{
					collab("bob", "alice"), collab("carol", "alice"), collab("carol", "dave"),
				},
				killed: map[string]bool{"bob-container-alice": true},
			},
			want: []string{
				"box bob-container: loginctl terminate-user bob-container-alice",
				"box bob-container: pkill -KILL -u bob-container-alice",
				"host: pkill -KILL -u bob-container-alice",
				"box carol-container: loginctl terminate-user carol-container-alice",
				"box carol-container: pkill -KILL -u carol-container-alice",
				"host: pkill -KILL -u carol-container-alice",
			},
			wantTerminated: 2, wantBoxes: 2,
		},
		{
			name: "user with no boxes",
			rec: &sessionRecorder{
				boxes:   map[string]string{"bob-container": "Running"},
				collabs: []*collaborator.Collaborator{collab("bob", "dave")},
			},
			want: nil, wantTerminated: 0, wantBoxes: 0,
		},
		{
			name: "stopped box: host account only",
			rec: &sessionRecorder{
				boxes: map[string]string{"alice-container": "Stopped"},
			},
			want:           []string{"host: pkill -KILL -u alice"},
			wantTerminated: 0, wantBoxes: 1,
		},
		{
			name: "collaborator row whose box is gone is skipped",
			rec: &sessionRecorder{
				boxes:   map[string]string{},
				collabs: []*collaborator.Collaborator{collab("bob", "alice")},
			},
			want: nil, wantTerminated: 0, wantBoxes: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := newSessionServer(tc.rec).EndUserSessions(context.Background(), "alice", "")
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(tc.rec.log, "\n") != strings.Join(tc.want, "\n") {
				t.Errorf("commands:\n%s\nwant:\n%s", strings.Join(tc.rec.log, "\n"), strings.Join(tc.want, "\n"))
			}
			for _, line := range tc.rec.log {
				for _, banned := range []string{"userdel", "rm ", "deluser", "authorized_keys", "sudoers"} {
					if strings.Contains(line, banned) {
						t.Errorf("terminate must not delete or edit anything: %q", line)
					}
				}
			}
			if resp.GetTerminatedSessions() != tc.wantTerminated || resp.GetBoxesTouched() != tc.wantBoxes {
				t.Errorf("response = %d terminated / %d boxes, want %d / %d",
					resp.GetTerminatedSessions(), resp.GetBoxesTouched(), tc.wantTerminated, tc.wantBoxes)
			}
		})
	}
}

// A failing collaborator lookup still ends the owner-box sessions it can
// reach, then reports the error so the caller (or assignment replay) retries.
func TestEndUserSessions_CollaboratorLookupErrorStillKillsOwnerBox(t *testing.T) {
	rec := &sessionRecorder{
		boxes:   map[string]string{"alice-container": "Running"},
		listErr: errors.New("db down"),
	}
	_, err := newSessionServer(rec).EndUserSessions(context.Background(), "alice", "")
	if err == nil {
		t.Fatal("expected the lookup error")
	}
	if len(rec.log) != 3 {
		t.Errorf("owner box sessions should still be ended: %v", rec.log)
	}
}

func TestEndUserSessions_RejectsInvalidUsername(t *testing.T) {
	for _, u := range []string{"", "-1", "a b", "../x", "alice-container-bob/../x"} {
		rec := &sessionRecorder{boxes: map[string]string{}}
		_, err := newSessionServer(rec).EndUserSessions(context.Background(), u, "")
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("%q: code = %v, want InvalidArgument", u, status.Code(err))
		}
		if len(rec.log) != 0 {
			t.Errorf("%q reached a command: %v", u, rec.log)
		}
	}
}

// TestTerminateUserSessions_Authorization pins the decision recorded on
// #2522: the admin role, or a service token (admin role) carrying ssh:write,
// may name any user; a tenant token only itself; another user is
// PermissionDenied.
func TestTerminateUserSessions_Authorization(t *testing.T) {
	cases := []struct {
		name     string
