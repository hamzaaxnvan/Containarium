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
		}
		var req SentinelKeyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid JSON body"}`, http.StatusBadRequest)
			return
		}
		pubKey := strings.TrimSpace(req.PublicKey)
		if pubKey == "" {
			http.Error(w, `{"error":"public_key is required"}`, http.StatusBadRequest)
			return
		}
		updated, rotated, err := a.SetSentinelKey(r.Context(), pubKey)
		if err != nil {
			log.Printf("[keys] sentinel-key authorize failed: %v", err)
			http.Error(w, `{"error":"failed to apply sentinel key"}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]int{"updated": updated, "rotated": rotated})
	}
}

// ServeAuthorizedKeysFromLister returns a handler that answers
// /authorized-keys from a ClientKeyLister and advertises sshPort as the
// node's SSH ingress (KeysResponse.SSHPort) so the sentinel forwards to the
// in-cluster gateway rather than the node's :22. Used on the K8s runtime.
func ServeAuthorizedKeysFromLister(lister ClientKeyLister, sshPort func() int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		m, err := lister.ListClientKeys(r.Context())
		if err != nil {
			log.Printf("[keys] lister failed: %v", err)
			http.Error(w, `{"error":"failed to list client keys"}`, http.StatusInternalServerError)
			return
		}
		keys := make([]UserKeys, 0, len(m))
		for username, ak := range m {
			content := strings.TrimSpace(ak)
			if content == "" {
				continue
			}
			keys = append(keys, UserKeys{Username: username, AuthorizedKeys: content})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(KeysResponse{Keys: keys, SSHPort: sshPort()})
	}
}

// ServeAuthorizedKeys returns an HTTP handler that walks
// homeRoot/*/.ssh/authorized_keys and returns each user's authorized keys
// as JSON for the sentinel to sync into sshpiper.
//
// homeRoot defaults to "/home" when empty (allows tests to inject a
// tmp dir).
//
// containerExistsFn is the orphan filter for #343: when non-nil, each
// candidate username is checked against the live container registry,
// and entries whose container is missing are dropped from the response
// AND logged as `WARNING orphan ...`. This prevents sshpiper from
// accepting keys for tenants whose container has been deleted (the
// resulting "Container <name>-container not found" inside the SSH
// session was the user-facing symptom in #343). Pass nil to disable
// the filter (back-compat / tests).
func ServeAuthorizedKeys(homeRoot string, containerExistsFn func(username string) bool, trustBundle TrustBundleProvider) http.HandlerFunc {
	if homeRoot == "" {
		homeRoot = "/home"
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		entries, err := os.ReadDir(homeRoot)
		if err != nil {
			log.Printf("[keys] failed to read %s: %v", homeRoot, err)
			http.Error(w, `{"error":"failed to read home directories"}`, http.StatusInternalServerError)
			return
		}

		var keys []UserKeys
		orphanCount := 0
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			username := entry.Name()
			akPath := filepath.Join(homeRoot, username, ".ssh", "authorized_keys")
			// akPath is built from os.ReadDir output (entry.Name() has
			// no path separators) joined with a fixed suffix — caller
			// can't inject a traversal.
			// #nosec G304 -- safe path construction (see above)
			data, err := os.ReadFile(akPath)
			if err != nil {
				continue // no authorized_keys file, skip
			}
			content := strings.TrimSpace(string(data))
			if content == "" {
				continue
			}
			if containerExistsFn != nil && !containerExistsFn(username) {
				// Only count/warn about accounts the orphan-reaper will
				// actually touch. reapOnce (orphan_reaper.go) refuses to
				// delete anything whose login shell isn't the
				// containarium-managed wrapper — a real host admin login
				// (e.g. the VM's own cloud-init "ubuntu" user) can share
				// this directory shape without ever being reap-eligible.
				// Without this gate the WARNING claims "orphan-reaper will
				// clean up on next tick" forever for an account the reaper
				// will never touch.
				if shell, shellErr := container.UserLoginShell(username); shellErr == nil && container.IsContainerManagedShell(shell) {
					orphanCount++
				}
				continue
			}
			keys = append(keys, UserKeys{
				Username:       username,
				AuthorizedKeys: content,
			})
		}
		if orphanCount > 0 {
			log.Printf("[keys] WARNING: %d orphan authorized_keys found (no live container); orphan-reaper will clean up on next tick", orphanCount)
		}

		resp := KeysResponse{Keys: keys}
		// SSHPort deliberately omitted (0): on the LXC runtime boxes are
		// reached on the node's own sshd port, which is the sentinel's
		// legacy 22/20022 convention.
		if trustBundle != nil {
			if content, version, ok := trustBundle(); ok {
				resp.TrustedUserCAKeys = content
				resp.SSHTrustVersion = version
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// sentinelKeyMarker is the comment line that flags the next line as the
// sentinel's current upstream pubkey in a user's authorized_keys file.
// applySentinelKey uses this marker to find and replace stale entries on
// rotation (sentinel VM replacement → new upstream keypair → old marker
// block must go, new one must take its place).
const sentinelKeyMarker = container.SentinelKeyMarker

// ServeSentinelKey returns an HTTP handler that accepts a POST with the
// sentinel's current upstream public key and installs it in every jump
// server user's authorized_keys file. Replaces any prior sentinel-marker
// block so that rotating the sentinel doesn't leave containers stranded
// with the old upstream key.
func ServeSentinelKey() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		var req SentinelKeyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid JSON body"}`, http.StatusBadRequest)
			return
		}

		pubKey := strings.TrimSpace(req.PublicKey)
		if pubKey == "" {
			http.Error(w, `{"error":"public_key is required"}`, http.StatusBadRequest)
			return
		}

		updated, rotated, err := applySentinelKey("/home", pubKey)
		if err != nil {
			log.Printf("[keys] sentinel-key apply failed: %v", err)
