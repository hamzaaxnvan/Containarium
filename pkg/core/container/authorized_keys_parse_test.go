//go:build !windows

package container

import (
	"strings"
	"testing"
)

// authorized_keys parsing (#1477).
//
// The bug: the parser returned on the FIRST valid key, so recovery restored a
// host account authorizing one key out of however many the container held.
// Every downstream check still looked healthy — account present, pipe built,
// diagnostic clean — and the operator whose key happened to sort second was
// simply refused. Which key survived was decided by file order.
//
// So the property under test is not "parses a key" but "returns ALL of them,
// in order".

const (
	keyA = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA operator@laptop"
	keyB = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB runner@ci"
	keyC = "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQCCCCCCCCCCCCCCCCCCCCCCCCCCCCC collaborator@desktop"
)

func TestParseAuthorizedKeysReturnsEveryKey(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []string
	}{
