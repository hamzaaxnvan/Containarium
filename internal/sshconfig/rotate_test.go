package sshconfig

import (
	"strings"
	"testing"
)

func TestRotateIdentity_PreservesHostsAndUnmanagedContent(t *testing.T) {
	before := "Host unrelated\n    IdentityFile ~/.ssh/personal\n\n"
	after := "\nHost elsewhere\n    IdentityFile ~/.ssh/other\n"
	managed := beginMarker + `
Host box
    HostName sentinel.example.com
    User box
    IdentityFile ~/.ssh/old
    IdentitiesOnly yes

Host box-jump
    HostName backend.example.com
    User alice
    IdentitiesOnly yes
` + endMarker + "\n"
	g, err := RotateIdentity(before+managed+after, "/home/alice user/.ssh/new")
	if err != nil {
		t.Fatal(err)
	}
	if g.Count != 2 || !strings.HasPrefix(g.Content, before) || !strings.HasSuffix(g.Content, after) {
		t.Fatalf("host count or unmanaged content changed: %+v", g)
	}
