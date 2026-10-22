package server

import (
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// TestCollaboratorKeysFromRequest covers the #369 back-compat resolution:
// repeated ssh_public_keys is preferred; the legacy single ssh_public_key
// is the fallback; neither set yields nil (the handler then rejects it).
func TestCollaboratorKeysFromRequest(t *testing.T) {
	tests := []struct {
		name string
		req  *pb.AddCollaboratorRequest
		want []string
	}{
		{
			name: "repeated preferred",
			req:  &pb.AddCollaboratorRequest{SshPublicKeys: []string{"ssh-ed25519 AAA a", "ssh-ed25519 BBB b"}},
			want: []string{"ssh-ed25519 AAA a", "ssh-ed25519 BBB b"},
		},
		{
			name: "legacy single fallback",
			req:  &pb.AddCollaboratorRequest{SshPublicKey: "ssh-ed25519 AAA a"},
			want: []string{"ssh-ed25519 AAA a"},
		},
		{
			name: "repeated wins over legacy when both set",
			req:  &pb.AddCollaboratorRequest{SshPublicKey: "ssh-ed25519 LEGACY x", SshPublicKeys: []string{"ssh-ed25519 AAA a"}},
			want: []string{"ssh-ed25519 AAA a"},
		},
		{
			name: "neither set yields nil",
			req:  &pb.AddCollaboratorRequest{},
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := collaboratorKeysFromRequest(tt.req)
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d (%v)", len(got), len(tt.want), got)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("key[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}
