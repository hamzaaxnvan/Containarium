//go:build integration

package anondoor

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tg123/sshpiper/libplugin"
	"golang.org/x/crypto/ssh"
)

const approvalInstruction = "Approve this device at https://approval.example.com, code TEST-2524"
const approvalPrompt = "press Enter once approved: "

// TestIntegration_ApprovalPluginProcess runs in a child process as the throwaway
// stdio plugin. The parent reuses its test binary, so no production command or
// separately built plugin is needed. Never write the test runner's PASS to the
// gRPC transport.
func TestIntegration_ApprovalPluginProcess(t *testing.T) {
	if os.Getenv("ANONDOOR_APPROVAL_PLUGIN") != "1" {
		return
	}
	key, err := os.ReadFile(os.Getenv("ANONDOOR_APPROVAL_UPSTREAM_KEY"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	p, err := libplugin.NewFromStdio(libplugin.SshPiperPluginConfig{
		NextAuthMethodsCallback: func(conn libplugin.ConnMetadata) ([]string, error) {
			if conn.GetMeta("approval_pending") == "true" {
				return []string{"keyboard-interactive"}, nil
			}
			return []string{"publickey"}, nil
		},
		PublicKeyCallback: func(_ libplugin.ConnMetadata, key []byte) (*libplugin.Upstream, error) {
			pub, err := ssh.ParsePublicKey(key)
			if err != nil || ssh.FingerprintSHA256(pub) != os.Getenv("ANONDOOR_APPROVAL_FINGERPRINT") {
				return nil, fmt.Errorf("unexpected client key")
			}
			return &libplugin.Upstream{Auth: libplugin.CreateRetryCurrentPluginAuth(map[string]string{
				"approval_pending": "true",
			})}, nil
		},
		KeyboardInteractiveCallback: func(conn libplugin.ConnMetadata, challenge libplugin.KeyboardInteractiveChallenge) (*libplugin.Upstream, error) {
			if conn.GetMeta("approval_pending") != "true" {
				return nil, fmt.Errorf("publickey must precede approval")
			}
			answer, err := challenge(conn.User(), approvalInstruction, approvalPrompt, false)
			if err != nil {
				return nil, err
			}
			if answer != "" {
				return nil, fmt.Errorf("expected Enter")
			}
			return &libplugin.Upstream{
				Uri: "tcp://" + os.Getenv("ANONDOOR_APPROVAL_UPSTREAM"), UserName: "approval-test",
				Auth: libplugin.CreatePrivateKeyAuth(key),
			}, nil
		},
	})
	if err == nil {
		err = p.Serve()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

// TestIntegration_PublicKeyPartialSuccessKeyboardInteractive characterizes
// libplugin v1.6.1 with a real sshpiperd and the stock OpenSSH client. It fails
// if the retry loses metadata, skips the key signature, hides the instruction,
// or cannot open an upstream session after the answer.
//
//	ANONDOOR_SSHPIPERD=/path/to/sshpiperd go test -tags integration -count=1 -v \
//	  ./internal/sentinel/anondoor -run '^TestIntegration_PublicKeyPartialSuccessKeyboardInteractive$'
func TestIntegration_PublicKeyPartialSuccessKeyboardInteractive(t *testing.T) {
	piperd := os.Getenv("ANONDOOR_SSHPIPERD")
	if piperd == "" {
		t.Skip("set ANONDOOR_SSHPIPERD to the v1.6.1 binary")
	}
	for _, tool := range []string{"ssh", "ssh-keygen"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatal(err)
