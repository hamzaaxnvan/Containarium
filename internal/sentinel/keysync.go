package sentinel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/footprintai/containarium/internal/gateway"
)

const (
	sshpiperConfigDir   = "/etc/sshpiper"
	sshpiperConfigFile  = "/etc/sshpiper/config.yaml"
	sshpiperUsersDir    = "/etc/sshpiper/users"
	sshpiperUpstreamKey = "/etc/sshpiper/upstream_key"

	// sshpiperTrustedUserCAKeys is where an operator installs the SSH
	// certificate-authority public key(s) that sshpiper should trust. When this
	// file exists and is non-empty, every generated pipe additionally accepts
	// CA-signed user certificates (sshpiper's trusted_user_ca_keys) — the
	// sentinel's sshpiperd is the ONE place a container-SSH user's key is
	// verified, so this is where certificate trust has to live. Absent = the
	// pre-existing authorized_keys-only behaviour, unchanged.
	sshpiperTrustedUserCAKeys = "/etc/sshpiper/trusted_user_ca_keys"
)

// backendKeys holds the users fetched from a single backend.
type backendKeys struct {
	backendID string
	backendIP string
	users     []gateway.UserKeys
	// sshPort is the SSH ingress the backend advertised in its
	// /authorized-keys response (KeysResponse.SSHPort). 0 = legacy
	// convention (22 direct / 20022 tunnel-loopback) — see routeTargetPort.
	sshPort  int
	lastSync time.Time
	lastErr  error
}

// userRoute is one resolved user→backend mapping that becomes a single sshpiper
// pipe. Package-level (not local to Apply) so renderSSHPiperConfig and its test
// can name it.
type userRoute struct {
	username       string
	authorizedKeys string
	backendIP      string
	sshPort        int // backend-advertised; 0 = legacy convention
}

// KeyStore syncs SSH authorized keys from one or more backends and generates
// sshpiper YAML configuration with per-user routing to the correct backend.
type KeyStore struct {
	mu            sync.RWMutex
	backends      map[string]*backendKeys // keyed by backend ID
	configChanged bool

	// caTrustContent/caTrustVersion cache the SSH CA trust bundle relayed
	// from whichever backend's /authorized-keys response last carried one
	// (cloud#1928: this sentinel has no cloud credential of its own, so it
	// picks the bundle up from a backend it already polls). "" version =
	// nothing cached yet — Apply leaves an existing (e.g. operator-dropped)
	// trusted_user_ca_keys file untouched in that case.
	caTrustContent string
	caTrustVersion string
}

// NewKeyStore creates a new KeyStore.
func NewKeyStore() *KeyStore {
	return &KeyStore{
		backends: make(map[string]*backendKeys),
	}
}

// Sync fetches authorized keys from a backend's /authorized-keys endpoint.
// Each backend's users are tracked separately for per-user routing.
func (ks *KeyStore) Sync(backendID, backendIP string, httpPort int) error {
	url := fmt.Sprintf("http://%s:%d/authorized-keys", backendIP, httpPort)

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := newSignedRequest(http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("key sync: build request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		ks.mu.Lock()
		ks.ensureBackendLocked(backendID, backendIP).lastErr = err
		ks.mu.Unlock()
		return fmt.Errorf("key sync GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("key sync: unexpected status %d from %s", resp.StatusCode, url)
		ks.mu.Lock()
		ks.ensureBackendLocked(backendID, backendIP).lastErr = err
		ks.mu.Unlock()
		return err
	}

	var keysResp gateway.KeysResponse
	if err := json.NewDecoder(resp.Body).Decode(&keysResp); err != nil {
		ks.mu.Lock()
		ks.ensureBackendLocked(backendID, backendIP).lastErr = err
		ks.mu.Unlock()
		return fmt.Errorf("key sync: decode response: %w", err)
	}

	ks.mu.Lock()
	bk := ks.ensureBackendLocked(backendID, backendIP)
	bk.users = keysResp.Keys
	bk.sshPort = keysResp.SSHPort
	bk.lastSync = time.Now()
	bk.lastErr = nil
	// Relay the SSH CA trust bundle (cloud#1928). Refuse an empty one: a
	// backend that hasn't completed its own first cloud heartbeat yet
	// reports nothing, and caching that over a good bundle would make Apply
	// remove trust from every certificate-auth login. A version match is
	// also a no-op — cheapest path, and keeps caTrustContent from being
	// reassigned (same string) on every poll.
	if keysResp.TrustedUserCAKeys != "" && keysResp.SSHTrustVersion != ks.caTrustVersion {
		ks.caTrustContent = keysResp.TrustedUserCAKeys
		ks.caTrustVersion = keysResp.SSHTrustVersion
	}
	ks.mu.Unlock()

	return nil
}

// BackendForLogin returns the id of the backend sshpiper routes login to,
// using exactly Apply's rule (the first backend, sorted by id, that lists
// the user wins). The SSH session shipper (#2415) uses it to send each
// record to the audit chain of the backend the session actually reached.
// ok is false when no synced backend lists the login.
func (ks *KeyStore) BackendForLogin(login string) (backendID string, ok bool) {
	if login == "" {
		return "", false
	}
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	ids := make([]string, 0, len(ks.backends))
	for id := range ks.backends {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		for _, u := range ks.backends[id].users {
			if u.Username == login {
				return id, true
			}
		}
	}
	return "", false
}

// SyncLegacy is the backward-compatible Sync that uses a default backend ID.
// Used in single-backend mode.
func (ks *KeyStore) SyncLegacy(backendIP string, httpPort int) error {
	return ks.Sync("default", backendIP, httpPort)
}

// RemoveBackend removes all user data for a backend.
func (ks *KeyStore) RemoveBackend(backendID string) {
	ks.mu.Lock()
	delete(ks.backends, backendID)
	ks.mu.Unlock()
}

// Apply writes the sshpiper YAML config with per-user routing.
// Each user is routed to the backend they were synced from.
// If the same username appears on multiple backends, GCP takes priority
// (lower backend priority value wins).
func (ks *KeyStore) Apply() error {
	ks.mu.RLock()
	// Build a merged user list with per-user backend IP routing
	seen := make(map[string]bool)
	var routes []userRoute

	// Collect from all backends — sort backend IDs for deterministic iteration
	// so the generated config is byte-stable and we can skip the rewrite when
	// nothing actually changed.
	backendIDs := make([]string, 0, len(ks.backends))
	for id := range ks.backends {
		backendIDs = append(backendIDs, id)
	}
	sort.Strings(backendIDs)
	for _, id := range backendIDs {
		bk := ks.backends[id]
		for _, u := range bk.users {
			if seen[u.Username] {
				continue // first backend to claim a user wins
			}
			seen[u.Username] = true
			routes = append(routes, userRoute{
				username:       u.Username,
				authorizedKeys: u.AuthorizedKeys,
				backendIP:      bk.backendIP,
				sshPort:        bk.sshPort,
			})
		}
	}
	ks.mu.RUnlock()

	// Sort routes by username for deterministic config output
	sort.Slice(routes, func(i, j int) bool {
		return routes[i].username < routes[j].username
	})

	if len(routes) == 0 {
		return fmt.Errorf("no users to configure")
	}

	// Ensure directories exist
	if err := os.MkdirAll(sshpiperUsersDir, 0755); err != nil { // #nosec G301 -- sshpiper needs world-readable dirs for authorized_keys lookup
		return fmt.Errorf("failed to create sshpiper users dir: %w", err)
	}

	// Write per-user authorized_keys
	liveUsers := make(map[string]bool, len(routes))
	for _, r := range routes {
		liveUsers[r.username] = true
		userDir := filepath.Join(sshpiperUsersDir, r.username)
		if err := os.MkdirAll(userDir, 0755); err != nil { // #nosec G301 -- sshpiper requires world-readable user dirs
			log.Printf("[keysync] failed to create dir for %s: %v", r.username, err)
			continue
		}
		akPath := filepath.Join(userDir, "authorized_keys")
		// Strip blank lines and comment lines before writing — sshpiper's
		// authorized_keys parser may stop at a blank line, causing key match failures.
		cleanedKeys := cleanAuthorizedKeys(r.authorizedKeys)
		if err := os.WriteFile(akPath, []byte(cleanedKeys+"\n"), 0600); err != nil {
			log.Printf("[keysync] failed to write authorized_keys for %s: %v", r.username, err)
			continue
		}
	}

	// Prune sshpiper user dirs for containers that no longer exist (#835).
	// The sentinel accumulates one dir per historical container; without this
	// step the directory grows without bound and keysync's O(dirs) work
	// widens the first-connect race window.
	pruned := 0
	if entries, err := os.ReadDir(sshpiperUsersDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() || liveUsers[e.Name()] {
				continue
			}
			stale := filepath.Join(sshpiperUsersDir, e.Name())
			if rmErr := os.RemoveAll(stale); rmErr != nil {
				log.Printf("[keysync] failed to prune stale sshpiper dir %s: %v", stale, rmErr)
			} else {
				pruned++
			}
		}
	}
	if pruned > 0 {
		log.Printf("[keysync] pruned %d stale sshpiper user dirs", pruned)
	}

	// Apply the relayed SSH CA trust bundle (cloud#1928), if any, BEFORE
	// rendering below — sshpiperCAKeysPath() gates purely on the file's
	// on-disk presence/size, so a first-ever cached bundle must land before
	// this same Apply() pass renders, not one cycle later. Best-effort: a
	// write failure here must not block the users/routes config update that
	// follows, which is why this ONLY logs rather than returning an error.
	if err := ks.applyCATrustBundle(); err != nil {
		log.Printf("[keysync] ssh ca trust bundle apply failed: %v", err)
	}

	// Generate sshpiper YAML config with per-user backend routing.
	newContent := renderSSHPiperConfig(routes, sshpiperCAKeysPath())

	// Compare with existing config
	oldContent, _ := os.ReadFile(sshpiperConfigFile)
	if bytes.Equal(oldContent, newContent) {
		ks.mu.Lock()
		ks.configChanged = false
		ks.mu.Unlock()
		return nil
	}

	if err := os.WriteFile(sshpiperConfigFile, newContent, 0600); err != nil {
		return fmt.Errorf("failed to write sshpiper config: %w", err)
	}

	ks.mu.Lock()
	ks.configChanged = true
	ks.mu.Unlock()

	log.Printf("[keysync] sshpiper config updated: %d users across %d backends", len(routes), ks.backendCount())
	return nil
}

// PushSentinelKey sends the sentinel's upstream public key to a backend.
func (ks *KeyStore) PushSentinelKey(backendIP string, httpPort int) error {
	pubKeyPath := sshpiperUpstreamKey + ".pub"
	pubKey, err := os.ReadFile(pubKeyPath)
	if err != nil {
		return fmt.Errorf("failed to read sentinel upstream public key %s: %w", pubKeyPath, err)
	}

	url := fmt.Sprintf("http://%s:%d/authorized-keys/sentinel", backendIP, httpPort)
	body, _ := json.Marshal(gateway.SentinelKeyRequest{PublicKey: strings.TrimSpace(string(pubKey))})

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := newSignedRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("push sentinel key: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("push sentinel key POST %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("push sentinel key: unexpected status %d", resp.StatusCode)
	}

	log.Printf("[keysync] sentinel upstream key pushed to backend %s", backendIP)
	return nil
}

// RunSyncLoop periodically syncs keys from a specific backend.
// Blocks until ctx is cancelled.
// A failed attempt is retried on the fast-retry schedule before falling
// back to the interval (#953), so a blip costs seconds of stale sshpiper
// routing rather than a full interval.
func (ks *KeyStore) RunSyncLoop(ctx context.Context, backendID, backendIP string, httpPort int, interval time.Duration) {
	log.Printf("[keysync] starting sync loop for backend %s (%s:%d, interval=%s)", backendID, backendIP, httpPort, interval)

	runSyncLoop(ctx, "keysync", interval, syncRetryDelays(interval), func() error {
		return ks.syncAndApply(backendID, backendIP, httpPort)
	})

	log.Printf("[keysync] sync loop stopped for backend %s", backendID)
}

// RunSyncLoopLegacy is backward-compatible: uses "default" as backend ID.
func (ks *KeyStore) RunSyncLoopLegacy(ctx context.Context, backendIP string, httpPort int, interval time.Duration) {
	ks.RunSyncLoop(ctx, "default", backendIP, httpPort, interval)
}

// syncAndApply pulls a backend's keys and rewrites the sshpiper config.
//
// The returned error reports whether this cycle fully succeeded, so the
// periodic loop can fast-retry a transient failure (#953). It deliberately
// does NOT retry internally: the event-driven resync endpoint calls this
// synchronously from an HTTP handler, and a retry burst there would block
// the response for minutes.
func (ks *KeyStore) syncAndApply(backendID, backendIP string, httpPort int) error {
	if err := ks.Sync(backendID, backendIP, httpPort); err != nil {
		log.Printf("[keysync] sync failed for %s: %v", backendID, err)
		if strings.Contains(err.Error(), "unexpected status 401") {
			log.Printf("[keysync] backend %s rejected the signed /authorized-keys request with 401 — "+
				"this backend's CONTAINARIUM_SENTINEL_AUTH_SECRET doesn't match this sentinel's (or is unset). "+
				"Until fixed, this backend never gets an sshpiper pipe entry and SSH to its containers silently "+
				"fails with 'no matching pipe' (#687). Fix: set matching CONTAINARIUM_SENTINEL_AUTH_SECRET on both ends "+
				"(BYOC hosts: `pool join --sentinel-auth-secret ...`).", backendID)
		}
		return err
