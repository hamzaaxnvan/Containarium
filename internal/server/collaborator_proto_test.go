package server

import (
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/collaborator"
)

// TestSplitSSHPublicKeys pins #1144's decode half: the stored value is a
// newline-joined blob (#369's AddCollaborator writes strings.Join(keys, "\n")
// into the one TEXT column) — this must recover the original list, and never
// choke on the single-key case that predates #369.
func TestSplitSSHPublicKeys(t *testing.T) {
	cases := []struct {
		name string
		blob string
		want []string
	}{
		{"empty", "", nil},
		{"single key, no newline", "ssh-ed25519 AAAAsingle", []string{"ssh-ed25519 AAAAsingle"}},
		{"two keys", "ssh-ed25519 AAAAlaptop\nssh-ed25519 AAAAdesktop", []string{"ssh-ed25519 AAAAlaptop", "ssh-ed25519 AAAAdesktop"}},
		{"blank lines dropped", "ssh-ed25519 AAAAone\n\nssh-ed25519 AAAAtwo\n", []string{"ssh-ed25519 AAAAone", "ssh-ed25519 AAAAtwo"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := splitSSHPublicKeys(tc.blob)
			if len(got) != len(tc.want) {
				t.Fatalf("splitSSHPublicKeys(%q) = %v, want %v", tc.blob, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("splitSSHPublicKeys(%q)[%d] = %q, want %q", tc.blob, i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestCollaboratorToProto is #1144's core fix: the response proto must carry
// every authorized key as ssh_public_keys, while the deprecated ssh_public_key
// scalar becomes a REAL single key (the first one) instead of the whole
// newline-joined blob it silently was before this field existed.
func TestCollaboratorToProto(t *testing.T) {
	now := time.Now()
	c := &collaborator.Collaborator{
		ID:                   "c1",
