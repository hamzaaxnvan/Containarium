package cmd

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/footprintai/containarium/internal/sshconfig"
	"github.com/footprintai/containarium/pkg/core/incus"
)

func resetSSHConfigGlobals(t *testing.T) {
	t.Helper()
	server, http, token := serverAddr, httpMode, authToken
	sentinel, port, identity, user := sshConfigSentinel, sshConfigPort, sshConfigIdentity, sshConfigUser
	jump := sshConfigJumpHost
	stopped, out, force := sshConfigIncludeStopped, sshConfigOutPath, sshConfigForce
	t.Cleanup(func() {
		serverAddr, httpMode, authToken = server, http, token
		sshConfigSentinel, sshConfigPort, sshConfigIdentity, sshConfigUser = sentinel, port, identity, user
		sshConfigJumpHost = jump
		sshConfigIncludeStopped, sshConfigOutPath, sshConfigForce = stopped, out, force
	})
	serverAddr, httpMode, authToken = "", false, ""
	sshConfigSentinel, sshConfigPort, sshConfigIdentity, sshConfigUser = "", 22, "", ""
	sshConfigJumpHost = ""
	sshConfigIncludeStopped, sshConfigOutPath, sshConfigForce = false, "", false
}

func TestSSHConfigOptions_RemoteSingleVM(t *testing.T) {
	for _, server := range []string{
		"http://vm.example.com:8080",
		"https://vm.example.com/api",
		"vm.example.com:50051",
		"vm.example.com",
		"http://[2001:db8::1]:8080",
		"[2001:db8::1]:50051",
	} {
		t.Run(server, func(t *testing.T) {
			resetSSHConfigGlobals(t)
			serverAddr = server
			sshConfigIdentity = "~/.ssh/containarium_ed25519"
			cs := []incus.ContainerInfo{{Name: "alice-container", Username: "alice", State: "Running", IPAddress: "10.0.3.100"}}
			g := sshconfig.Generate(cs, sshConfigOptions())
			host := "vm.example.com"
			if strings.Contains(server, "2001:db8") {
				host = "2001:db8::1"
			}
			want := "Host alice-container\n" +
