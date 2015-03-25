package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/footprintai/containarium/pkg/core/container"
)

// UserKeys holds a username and their authorized_keys content.
type UserKeys struct {
	Username       string `json:"username"`
	AuthorizedKeys string `json:"authorized_keys"`
}

// KeysResponse is the JSON response from the /authorized-keys endpoint.
type KeysResponse struct {
	Keys []UserKeys `json:"keys"`

	// SSHPort is the port on this node the sentinel should forward SSH to
	// for every user in Keys. 0 (or absent — pre-advertisement daemons)
	// means the legacy convention: 22 direct, 20022 through a tunnel
	// loopback. A K8s-runtime node advertises its in-cluster gateway
	// ingress here (e.g. the sshpiper Service's NodePort), since nothing
	// listens on the node's :22 for boxes.
	SSHPort int `json:"ssh_port,omitempty"`

	// TrustedUserCAKeys, when non-empty, is the SSH CA trust-bundle file
	// content (sshd TrustedUserCAKeys form) this node's cloud client has
	// cached — see internal/cloud.Client.SSHTrustedUserCAKeys and #1928. A
	// sentinel with no cloud credential of its own relays this from
	// whichever backend it polls, the same way it already relays
	// authorized_keys. Empty/absent = this node has nothing cached (no
	// cloud enrollment, no SSH CA configured, or not yet heartbeated) —
	// the sentinel must leave whatever it already has on disk untouched,
	// never clear it.
	TrustedUserCAKeys string `json:"trusted_user_ca_keys,omitempty"`
	// SSHTrustVersion is the version TrustedUserCAKeys was cached at, so
	// the sentinel can skip rewriting the file + reapplying sshpiper's
	// config when it already matches (cheap, frequent polling stays cheap).
	SSHTrustVersion string `json:"ssh_trust_version,omitempty"`
}

// TrustBundleProvider returns the most recently cached SSH CA trust-bundle
// file content and version for /authorized-keys to advertise, or ok=false
// when nothing is cached yet. The LXC daemon wires this to
// cloud.Client.SSHTrustedUserCAKeys; nil (no cloud enrollment, or the K8s
// runtime, which has no sshd to hold TrustedUserCAKeys — see cloud#1122) =
// never advertise a bundle.
type TrustBundleProvider func() (content, version string, ok bool)

// SentinelKeyRequest is the JSON body for POST /authorized-keys/sentinel.
type SentinelKeyRequest struct {
	PublicKey string `json:"public_key"`
}

// ClientKeyLister is the runtime-neutral source of tenant → authorized_keys
// for /authorized-keys. The LXC runtime uses the home-dir walker
// (ServeAuthorizedKeys); the K8s runtime supplies a lister backed by box
// metadata (nothing lives in /home on a K8s node).
type ClientKeyLister interface {
	ListClientKeys(ctx context.Context) (map[string]string, error)
}

// SentinelKeyAuthorizer installs the sentinel's upstream pubkey at whatever
// hop authenticates the sentinel on this runtime. The LXC runtime writes it
// into every box's authorized_keys (ServeSentinelKey); the K8s runtime
// authorizes it at the in-cluster gateway (the node's sshpiper Pipes).
type SentinelKeyAuthorizer interface {
	// SetSentinelKey persists pubKey and applies it. Returns how many boxes
	// were (re)authorized and how many had a prior different sentinel key
	// replaced.
	SetSentinelKey(ctx context.Context, pubKey string) (updated, rotated int, err error)
}

// ServeSentinelKeyWithAuthorizer returns a /authorized-keys/sentinel handler
// backed by a SentinelKeyAuthorizer (the K8s runtime path).
func ServeSentinelKeyWithAuthorizer(a SentinelKeyAuthorizer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
