package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/footprintai/containarium/internal/credentials"
	"github.com/footprintai/containarium/internal/sshconfig"
	"github.com/footprintai/containarium/internal/sshkey"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/spf13/cobra"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Sub-tasks A5 + A6 of umbrella-issue #100: per-machine SSH key
// registration with the cloud's UserService.
//
// See prd/cloud/cli-login-and-multi-env-ssh.md §"Design —
// Multi-environment SSH" for the contract:
//
//   containarium ssh setup [--name=<friendly>] [--key=<path>]
//       reads/generates local key, POSTs UserService.AddSSHKey
//   containarium ssh list
//       GETs UserService.ListSSHKeys, prints a table
//   containarium ssh remove <name>
//       DELETEs UserService.RemoveSSHKey by name
//   containarium ssh propagate
//       POSTs UserService.PropagateSSHKeysToBoxes (cloud-side RPC
//       not yet implemented; the CLI handles Unimplemented
//       gracefully, matching the pattern from PR #297 / `containarium
//       ttl`).
//
// Strong typing per CLAUDE.md: the on-wire shape is sshkey.SSHKey,
// not map[string]any. The HTTP shim around UserService is small enough
// that we inline it here rather than refactor internal/client/http.go
// — those wrappers expect already-authenticated gRPC plumbing pointed
// at the daemon, whereas the SSH-keys endpoints live on the cloud
// (the same surface login.go talks to).

// Per-command flags.
var (
	sshSetupName       string
	sshSetupKeyPath    string
	sshSetupGenerate   bool
	sshSetupForce      bool
	sshSetupServer     string
	sshListServer      string
	sshRemoveServer    string
	sshPropagateServer string
	sshPropagateBoxes  []string
	sshRotateName      string
	sshRotateServer    string
)

var sshCmd = &cobra.Command{
	Use:   "ssh",
	Short: "Manage per-machine SSH keys registered with your account",
	Long: `Register, list, and remove the SSH public key(s) the cloud knows about
for your user. Once a key is registered with ` + "`containarium ssh setup`" + `,
it gets installed in the authorized_keys file of every box you create
(or, with ` + "`containarium ssh propagate`" + `, every box you already own).

The key registration lives on the cloud's UserService, NOT on a single
daemon — so it follows you across self-hosted instances. This is the
"one laptop, many boxes" half of the multi-environment SSH design.

Typical first-run, post-login flow:

  containarium login                  # populates ~/.containarium/credentials.json
  containarium ssh setup              # uploads ~/.ssh/id_ed25519.pub (or generates one)
  containarium create alice ...       # the key is auto-installed in alice's authorized_keys
  ssh alice                           # works

To register the same machine's key without re-running setup on every
fresh box, run ` + "`containarium ssh propagate`" + ` once.`,
}

var sshSetupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Register this machine's SSH public key with the cloud",
	Long: `Locate (or generate) the local SSH keypair, then upload the public
half to the cloud via UserService.AddSSHKey. The private key stays
on your laptop.

Behavior:

  - With no flags, looks for ~/.ssh/id_ed25519.pub (then id_rsa.pub).
    If neither exists, generates a fresh ed25519 key at
    ~/.ssh/containarium_ed25519{,.pub} and uploads the public half.

  - --key=<path> uses an explicit public-key file (overrides the
    search).

  - --name=<label> sets the friendly name shown by ` + "`containarium ssh list`" + `.
    Defaults to "<user>@<host>".

  - --generate forces generation of a new keypair even if one
    already exists in ~/.ssh.

  - --force allows --generate to overwrite an existing
    containarium-prefixed key.

Requires ` + "`containarium login`" + ` first (the upload is
authenticated with the credentials-file token).`,
	Example: `  # Default: find ~/.ssh/id_ed25519.pub and register as alice@laptop
  containarium ssh setup

  # Custom label
  containarium ssh setup --name=alice-mbp

  # Explicit key file
  containarium ssh setup --key=~/.ssh/work_ed25519.pub

  # Generate a fresh containarium-only key
  containarium ssh setup --generate`,
	RunE: runSSHSetup,
}

var sshListCmd = &cobra.Command{
	Use:   "list",
	Short: "List SSH keys registered with your account",
	Long: `Print the keys the cloud knows about for your user, one per row,
showing the friendly name, the SHA256 fingerprint, and when each
key was registered.

Pipes-friendly: passes through stdout as a tab-aligned table; the
key column is the fingerprint, not the full public-key blob.`,
	RunE: runSSHList,
}

var sshRotateCmd = &cobra.Command{
	Use:   "rotate",
	Short: "Replace this device's SSH key with proof of possession",
	Long:  "Generate a fresh Ed25519 key, sign a server challenge, and register it under this device's name. After registration succeeds, update the managed SSH config and keep the old key during the server's overlap window.",
	Args:  cobra.NoArgs,
	RunE:  runSSHRotate,
}

var sshRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove a registered SSH key by friendly name",
	Long: `Delete the SSH key with the given name from the cloud's record.
Future boxes you create will not include this key in their
authorized_keys (existing boxes are NOT touched — use
` + "`containarium ssh propagate`" + ` to push the updated key set).

The <name> argument is matched against the friendly label you set
with ` + "`containarium ssh setup --name=`" + `. List the available
names with ` + "`containarium ssh list`" + `.`,
	Args: cobra.ExactArgs(1),
	RunE: runSSHRemove,
}

var sshPropagateCmd = &cobra.Command{
	Use:   "propagate",
	Short: "Push your registered SSH keys to every existing box you own",
	Long: `Walk every container you own and update its authorized_keys to
match the keys currently registered with ` + "`containarium ssh list`" + `.

Useful after:

  - Adding a new laptop with ` + "`containarium ssh setup`" + ` —
    propagate so the new key gets installed retroactively.

  - Removing a lost laptop's key with ` + "`containarium ssh remove`" + ` —
    propagate so the removal takes effect on existing boxes.

This invokes UserService.PropagateSSHKeysToBoxes on the cloud, which
fans out to every daemon hosting one of your boxes. The cloud-side
RPC is not yet implemented (see the "what's NOT in this PR" section
of the PR description); the CLI handles the resulting Unimplemented
gracefully (prints a clear warning, exits 0) so callers can wire
this into their workflow today.`,
	Example: `  # Push to every owned box
  containarium ssh propagate

  # Limit to a specific subset of boxes
  containarium ssh propagate --box=alice --box=bob`,
	RunE: runSSHPropagate,
}

func init() {
	sshRotateCmd.Flags().StringVar(&sshRotateName, "name", "", "device key name (default: <user>@<host>)")
	sshRotateCmd.Flags().StringVar(&sshRotateServer, "server", "", "server URL (default: default_server)")
	sshCmd.AddCommand(sshRotateCmd)
	sshSetupCmd.Flags().StringVar(&sshSetupName, "name", "", "friendly label for this key (default: <user>@<host>)")
	sshSetupCmd.Flags().StringVar(&sshSetupKeyPath, "key", "", "path to public key file (default: auto-detect ~/.ssh/id_ed25519.pub or generate)")
	sshSetupCmd.Flags().BoolVar(&sshSetupGenerate, "generate", false, "always generate a fresh ed25519 keypair (default: re-use ~/.ssh/id_ed25519.pub if present)")
	sshSetupCmd.Flags().BoolVar(&sshSetupForce, "force", false, "with --generate, allow overwriting an existing containarium_ed25519 key")
	sshSetupCmd.Flags().StringVar(&sshSetupServer, "server", "", "server URL for the upload (default: default_server from credentials file, then "+defaultLoginServer+")")

	sshListCmd.Flags().StringVar(&sshListServer, "server", "", "server URL to query (default: default_server)")
	sshRemoveCmd.Flags().StringVar(&sshRemoveServer, "server", "", "server URL to target (default: default_server)")
	sshPropagateCmd.Flags().StringVar(&sshPropagateServer, "server", "", "server URL to target (default: default_server)")
	sshPropagateCmd.Flags().StringSliceVar(&sshPropagateBoxes, "box", nil, "restrict propagation to these box names (repeatable); default: all owned boxes")

	sshCmd.AddCommand(sshSetupCmd)
	sshCmd.AddCommand(sshListCmd)
	sshCmd.AddCommand(sshRemoveCmd)
	sshCmd.AddCommand(sshPropagateCmd)
	rootCmd.AddCommand(sshCmd)
}

// pickSSHServer collapses the precedence chain for the SSH-key
// endpoints. Mirrors pickLoginServer's intent but adds a
// credentials-file fallback because we usually want the same server
// the user is logged into:
//
//  1. explicit --server flag
//  2. credentials-file default_server
//  3. defaultLoginServer constant
func pickSSHServer(explicit string) string {
	if explicit != "" {
		return canonicalServerAddr(strings.TrimRight(explicit, "/"))
	}
	if path, err := credentials.DefaultPath(); err == nil {
		if cf, err := credentials.Load(path); err == nil && cf.DefaultServer != "" {
			return strings.TrimRight(cf.DefaultServer, "/")
		}
	}
	return defaultLoginServer
}

// sshHTTPClient is a small wrapper that talks to the cloud's
// UserService REST endpoints with the bearer token from the
// credentials store. Kept package-local rather than reaching into
// internal/client/http.go because that wrapper is daemon-shaped
// (gRPC-gateway over an mTLS-fronted daemon) — wrong surface for the
// cloud's user-keys endpoints.
type sshHTTPClient struct {
	hc     *http.Client
	server string
	token  string
}

func newSSHHTTPClient(server string) (*sshHTTPClient, error) {
	tok := resolveAuthToken(server)
	if tok == "" {
		return nil, noTokenError(server)
	}
	return &sshHTTPClient{
		hc:     &http.Client{Timeout: 30 * time.Second},
		server: server,
		token:  tok,
	}, nil
}

// doJSON sends method+path with optional body, decodes the response
// into out (if non-nil), and converts 501 Not Implemented into a gRPC
// Unimplemented status so the call-site can lean on isUnimplemented()
// (defined in ttl.go).
func (c *sshHTTPClient) doJSON(ctx context.Context, method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.server+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusNotImplemented {
		return status.Errorf(codes.Unimplemented, "%s %s: server returned 501 Not Implemented", method, path)
	}
	if resp.StatusCode == http.StatusNotFound {
		// grpc-gateway emits 404 for routes the cloud hasn't wired
		// yet. Treat that as Unimplemented too so the propagate path
		// can fall through gracefully — matching ttl.go's posture.
		return status.Errorf(codes.Unimplemented, "%s %s: route not found (server may not support this RPC yet)", method, path)
	}
	if resp.StatusCode == http.StatusConflict {
		// 409 = the resource already exists (e.g. this SSH key is already
		// registered). Surface it as a typed AlreadyExists so callers can
		// treat re-registration as idempotent success rather than a failure.
		return status.Errorf(codes.AlreadyExists, "%s %s: %s", method, path, strings.TrimSpace(string(rb)))
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%s %s: status %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(rb)))
	}

	if out != nil && len(rb) > 0 {
		if err := json.Unmarshal(rb, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// Wire-format DTOs for the UserService SSH-key endpoints. Each
// mirrors the shipped cloud JSON shape. We redeclare so the OSS
// CLI compiles without depending on a cloud-private pb package.
//
// Per CLAUDE.md: typed structs, not map[string]any.

type addSSHKeyReq struct {
	Name        string         `json:"name"`
	PublicKey   string         `json:"public_key"`
	KeyType     sshKeyTypeJSON `json:"key_type,omitempty"`
	ChallengeID string         `json:"challenge_id,omitempty"`
	Signature   []byte         `json:"signature,omitempty"`
}

// Use proto enum names for the cloud JSON representation; retain the enum's type
// internally instead of storing an independently invented algorithm string.
type sshKeyTypeJSON pb.SSHKeyType

func (t sshKeyTypeJSON) MarshalJSON() ([]byte, error) {
	return json.Marshal(pb.SSHKeyType(t).String())
}

func (t *sshKeyTypeJSON) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err != nil {
		return err
	}
	value, ok := pb.SSHKeyType_value[name]
	if !ok {
		return fmt.Errorf("unknown SSH key type %q", name)
	}
	*t = sshKeyTypeJSON(value)
	return nil
}

// A []byte nonce uses protobuf JSON's Base64 representation.
type sshKeyChallengeReq struct{}
type sshKeyChallengeResp struct {
	ChallengeID string `json:"challengeId"`
	Nonce       []byte `json:"nonce"`
}

func (challenge *sshKeyChallengeResp) UnmarshalJSON(data []byte) error {
	type challengeJSON sshKeyChallengeResp
	var wire struct {
		challengeJSON
		ChallengeID string `json:"challenge_id"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*challenge = sshKeyChallengeResp(wire.challengeJSON)
	if challenge.ChallengeID == "" {
		challenge.ChallengeID = wire.ChallengeID
	}
	return nil
}

type addSSHKeyResp struct {
	Key sshkey.SSHKey `json:"key"`
}

type listSSHKeysResp struct {
	Keys []sshkey.SSHKey `json:"keys"`
}

type propagateReq struct {
	Boxes []string `json:"boxes,omitempty"` // empty = all owned
}

type propagateResp struct {
	UpdatedBoxes []string `json:"updated_boxes"`
	SkippedBoxes []string `json:"skipped_boxes"`
}

// AddSSHKey uploads pubkey under the given name. Returns the
// canonicalised SSHKey (with fingerprint + created_at) the server
// stored.
func (c *sshHTTPClient) AddSSHKey(ctx context.Context, name, pub string) (*sshkey.SSHKey, error) {
	var out addSSHKeyResp
	if err := c.doJSON(ctx, http.MethodPost, "/v1/user/ssh-keys", addSSHKeyReq{Name: name, PublicKey: pub, KeyType: sshKeyTypeJSON(sshkey.ParseType(pub))}, &out); err != nil {
		return nil, err
	}
	return &out.Key, nil
