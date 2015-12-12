//go:build integration && !windows

package sshauthz

// A real sshpiperd drives the production CLI plugins, then its real yaml
// routing plugin, against a stub daemon and an in-process SSH upstream.
// SSH_AUTHZ_SSHPIPERD, SSH_AUTHZ_YAML and SSH_AUTHZ_CONTAINARIUM point to the
// three binaries. No deployment, cloud account, or external sshd is required.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/sentinel/sshsession"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"golang.org/x/crypto/ssh"
	"google.golang.org/protobuf/encoding/protojson"
)

// integrationChain is one running sshpiperd with the production authz and
// session plugins and the real yaml plugin, routing login alice to a loopback
// upstream. allowKey is ALLOWed by the stub daemon; denyKey is denied with
// UNKNOWN_KEY. Both are in alice's static authorized_keys, so only the authz
// plugin can tell them apart.
type integrationChain struct {
	address, records, dir string
	upstream              string
	allowKey, denyKey     ssh.Signer
	outage                *atomic.Bool
	calls                 *atomic.Int32
	// respond, when set, answers every authorize request instead of the
	// allowKey/denyKey rule.
	respond *atomic.Pointer[func(*pb.AuthorizeSSHRequest) *pb.AuthorizeSSHResponse]
}

func startIntegrationChain(t *testing.T, authzArgs ...string) *integrationChain {
	t.Helper()
	piperd, yaml, ctn := os.Getenv("SSH_AUTHZ_SSHPIPERD"), os.Getenv("SSH_AUTHZ_YAML"), os.Getenv("SSH_AUTHZ_CONTAINARIUM")
	if piperd == "" || yaml == "" || ctn == "" {
		t.Skip("set SSH_AUTHZ_SSHPIPERD, SSH_AUTHZ_YAML, SSH_AUTHZ_CONTAINARIUM")
	}
	dir := t.TempDir()
	hostKey, upstreamKey, denyKeyFile := filepath.Join(dir, "host_key"), filepath.Join(dir, "upstream_key"), filepath.Join(dir, "deny_key")
	for _, path := range []string{hostKey, upstreamKey, denyKeyFile} {
		if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", path).CombinedOutput(); err != nil {
			t.Fatalf("ssh-keygen: %v %s", err, out)
		}
	}
	raw, err := os.ReadFile(hostKey)
	if err != nil {
		t.Fatal(err)
	}
	hostSigner, err := ssh.ParsePrivateKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	upstream := integrationSSHServer(t, hostSigner)
	// denyKey lives on disk so a stock OpenSSH client can offer it too.
	raw, err = os.ReadFile(denyKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	denyKey, err := ssh.ParsePrivateKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	// allowKey is on disk too, passphrase-protected, so OpenSSH in batch mode
	// can offer its public half but never sign with it.
	const allowPassphrase = "integration-only"
	allowKeyFile := filepath.Join(dir, "allow_key")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", allowPassphrase, "-f", allowKeyFile).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v %s", err, out)
	}
	raw, err = os.ReadFile(allowKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	allowKey, err := ssh.ParsePrivateKeyWithPassphrase(raw, []byte(allowPassphrase))
	if err != nil {
		t.Fatal(err)
	}
	allowFP := ssh.FingerprintSHA256(allowKey.PublicKey())
	keysFile := filepath.Join(dir, "authorized_keys")
	if err := os.WriteFile(keysFile, append(ssh.MarshalAuthorizedKey(allowKey.PublicKey()), ssh.MarshalAuthorizedKey(denyKey.PublicKey())...), 0600); err != nil {
		t.Fatal(err)
	}
	caFile := filepath.Join(dir, "trusted_user_ca_keys")
	if err := os.WriteFile(caFile, ssh.MarshalAuthorizedKey(testKey(t).PublicKey()), 0600); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(dir, "config.yaml")
	config := fmt.Sprintf("version: \"1.0\"\npipes:\n  - from:\n      - username: alice\n        authorized_keys:\n          - %s\n        trusted_user_ca_keys:\n          - %s\n    to:\n      username: alice\n      host: %s\n      private_key: %s\n      ignore_hostkey: true\n", keysFile, caFile, upstream, upstreamKey)
	if err := os.WriteFile(configFile, []byte(config), 0400); err != nil {
		t.Fatal(err)
	}

	var outage atomic.Bool
	var calls atomic.Int32
	var respond atomic.Pointer[func(*pb.AuthorizeSSHRequest) *pb.AuthorizeSSHResponse]
	secret := []byte("integration-sentinel-secret-32-bytes")
	verifier := auth.NewSentinelVerifier(nil, secret)
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != AuthorizePath || r.Method != http.MethodPost {
			t.Error("wrong daemon request")
			w.WriteHeader(404)
			return
		}
		if err := verifier.VerifyRequest(r, time.Now()); err != nil {
			t.Errorf("signature: %v", err)
			w.WriteHeader(401)
			return
		}
		if outage.Load() {
			w.WriteHeader(503)
			return
		}
		var wire json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		req := &pb.AuthorizeSSHRequest{}
		if err := protojson.Unmarshal(wire, req); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		res := &pb.AuthorizeSSHResponse{Decision: pb.SSHAuthzDecision_SSH_AUTHZ_DECISION_DENY, DenyReason: pb.SSHDenyReason_SSH_DENY_REASON_UNKNOWN_KEY}
		if req.GetCredential().GetPublicKey().GetFingerprintSha256() == allowFP {
			res = allowed("user-allow")
		}
		if fn := respond.Load(); fn != nil {
			res = (*fn)(req)
		}
		raw, _ := protojson.Marshal(res)
		_, _ = w.Write(raw)
	}))
	t.Cleanup(daemon.Close)
	socket := filepath.Join(dir, "authz.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	resolver := resolverFunc(func(_ context.Context, login string) (string, bool, error) {
		return "backend-test", login == "alice", nil
	})
	authorizer := &HTTPAuthorizer{BackendURL: func(string) (string, error) { return daemon.URL, nil }, Sign: func(r *http.Request) { auth.SignSentinelRequest(r, secret) }}
	bridge := &http.Server{Handler: BridgeHandler(resolver, authorizer), ReadHeaderTimeout: time.Second}
	t.Cleanup(func() { _ = bridge.Close() })
	go func() { _ = bridge.Serve(ln) }()
	records := filepath.Join(dir, "sessions.jsonl")
	port := integrationFreePort(t)
	logFile, err := os.Create(filepath.Join(dir, "piper.log"))
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"-l", "127.0.0.1", "-p", strconv.Itoa(port), "-i", hostKey, "--drop-hostkeys-message",
		ctn, "sentinel", "ssh-authz-plugin", "--ssh-authz-socket", socket, "--ssh-authz-grace", "2s", "--records-file", records, "--sshpiper-config", configFile}
	args = append(args, authzArgs...)
	args = append(args, "--",
