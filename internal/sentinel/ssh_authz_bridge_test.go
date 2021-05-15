//go:build !windows

package sentinel

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/gateway"
	"github.com/footprintai/containarium/internal/sentinel/sshauthz"
)

func TestSSHAuthzBridgeUsesKeyStoreAndPrivateSocket(t *testing.T) {
	ks := NewKeyStore()
	ks.backends["z"] = &backendKeys{backendID: "z", users: []gateway.UserKeys{{Username: "alice"}}}
	ks.backends["a"] = &backendKeys{backendID: "a", users: []gateway.UserKeys{{Username: "alice"}}}
	manager := &Manager{keyStore: ks}
	path := filepath.Join(t.TempDir(), "authz.sock")
	stop, err := manager.startSSHAuthzBridge(path)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatalf("socket mode: %v %v", st, err)
	}
	client := sshauthz.NewBridgeClient(path)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	b, known, err := client.ResolveBackend(ctx, "alice")
	if err != nil || !known || b != "a" {
