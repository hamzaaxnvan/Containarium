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
