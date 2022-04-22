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
		wantLog  string
	}{
		{name: "flag absent warns", cmdline: with(), wantWarn: true, wantLog: "--login-grace-time"},
		{name: "space form 90s is fine", cmdline: with("--login-grace-time", "90s")},
		{name: "equals form 90s is fine", cmdline: with("--login-grace-time=90s")},
		{name: "single dash space form", cmdline: with("-login-grace-time", "90s")},
		{name: "single dash equals form", cmdline: with("-login-grace-time=90s")},
		{name: "exactly cap plus margin is fine", cmdline: with("--login-grace-time=70s")},
		{name: "30s warns", cmdline: with("--login-grace-time", "30s"), wantWarn: true, wantLog: "30s"},
		{name: "just under the minimum warns", cmdline: with("--login-grace-time=69s"), wantWarn: true},
		{name: "unparsable value warns", cmdline: with("--login-grace-time=abc"), wantWarn: true},
		{name: "flag without value warns", cmdline: nul("sshpiperd", "--login-grace-time"), wantWarn: true},
		{name: "unreadable cmdline does not warn", err: errors.New("no /proc")},
		{name: "empty cmdline does not warn", cmdline: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var warns []string
			CheckParentLoginGrace(func() ([]byte, error) { return tc.cmdline, tc.err }, func(f string, a ...any) {
				warns = append(warns, strings.TrimSpace(fmt.Sprintf(f, a...)))
			})
