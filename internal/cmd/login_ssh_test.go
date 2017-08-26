package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/credentials"
	"github.com/footprintai/containarium/internal/sshkey"
)

// Tests for sub-task A7 (umbrella-issue #100): the
// `containarium login --with-ssh-setup / --no-ssh-setup` integration
// landed in login.go's runLogin tail-call to maybeRunPostLoginSSHSetup.
//
// These tests intentionally live in a NEW file (per the brief's
// "lesson from Phase 1: don't modify shared test files"). They share
// the cmd-package globals with ssh_test.go and login_test.go but
// redeclare nothing — withSSHHome (defined in ssh_test.go) is reused
// because its cleanup already resets the ssh-setup-related flag
// globals.

// withSSHLoginHome stacks ssh_test.go's withSSHHome with extra
// cleanup for the A7-only flag globals (loginWithSSHSetup,
// loginNoSSHSetup, loginPromptReader).
func withSSHLoginHome(t *testing.T) string {
	t.Helper()
	home := withSSHHome(t)
	t.Cleanup(func() {
		loginWithSSHSetup = false
		loginNoSSHSetup = false
		loginPromptReader = nil
		loginServer = ""
		loginNoBrowser = false
		loginDeviceName = ""
	})
	return home
}

// fakeLoginAndSSHKeysServer wires the device-flow endpoints from
// login.go AND the SSH-keys endpoints from ssh.go on a single
// httptest.Server, because the post-login auto-setup path expects to
// hit the same host the login ran against.
type fakeLoginAndSSHKeysServer struct {
	srv           *httptest.Server
	addedKeys     []addSSHKeyReq
	keys          []sshkey.SSHKey
	approveAfter  int32
	pollCount     int32
	sawAddSSHKey  atomic.Bool
	receivedToken string
}
