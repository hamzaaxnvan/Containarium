package sshauthz

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func nul(args ...string) []byte { return []byte(strings.Join(args, "\x00") + "\x00") }

func TestCheckParentLoginGrace(t *testing.T) {
	base := []string{"/usr/local/bin/sshpiperd", "-i", "/etc/sshpiper/host_key", "-p", "22"}
	with := func(extra ...string) []byte {
		return nul(append(append(append([]string{}, base...), extra...), "containarium", "sentinel", "ssh-authz-plugin")...)
	}
	tests := []struct {
		name     string
		cmdline  []byte
		err      error
		wantWarn bool
