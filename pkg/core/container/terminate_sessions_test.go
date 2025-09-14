package container

import (
	"errors"
	"testing"
)

// TestTerminateAccountSessions_KillsWithoutDeleting pins the shared session
// kill (#2522): loginctl terminate-user, then pkill -KILL -u, inside the box,
// and nothing else — no userdel, no sudoers or authorized_keys edit.
func TestTerminateAccountSessions_KillsWithoutDeleting(t *testing.T) {
	cases := []struct {
		name      string
		pkillExit int
		pkillErr  error
		want      bool
	}{
		{name: "processes killed", pkillExit: 0, want: true},
		{name: "nothing to kill", pkillExit: 1, want: false},
		{name: "exec failed", pkillErr: errors.New("box unreachable"), want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &recordingBackend{pkillExit: tc.pkillExit, pkillErr: tc.pkillErr}
			m := NewWithBackend(fake)

			got := m.TerminateAccountSessions("owner-container", "owner-container-alice")
			if got != tc.want {
				t.Errorf("TerminateAccountSessions = %v, want %v", got, tc.want)
			}
			want := [][]string{
				{"loginctl", "terminate-user", "owner-container-alice"},
				{"pkill", "-KILL", "-u", "owner-container-alice"},
			}
			if len(fake.calls) != len(want) {
				t.Fatalf("calls = %v, want %v", fake.calls, want)
			}
