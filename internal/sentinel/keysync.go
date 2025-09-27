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
