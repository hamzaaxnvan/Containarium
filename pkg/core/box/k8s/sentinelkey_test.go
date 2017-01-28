//go:build k8s

package k8s

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/footprintai/containarium/pkg/core/box"
)

// TestSetSentinelKeyAppendsToPipes: authorizing the sentinel key adds it to
// every box's Pipe from-keys alongside the agent's key, and persists it in
// the gateway-namespace Secret so it survives a daemon restart.
func TestSetSentinelKeyAppendsToPipes(t *testing.T) {
	b, cs, _ := gatewayUpstreamBackend()
	ctx := context.Background()
	if _, err := b.Create(ctx, box.BoxSpec{
		Ref:     box.BoxRef{Tenant: "alice"},
		Image:   "x",
		SSHKeys: []string{"ssh-ed25519 AGENTKEY agent@laptop"},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	updated, rotated, err := b.SetSentinelKey(ctx, "ssh-ed25519 SENTINELKEY sentinel@fleet")
	if err != nil {
		t.Fatalf("SetSentinelKey: %v", err)
	}
	if updated != 1 || rotated != 0 {
		t.Errorf("SetSentinelKey = (updated %d, rotated %d), want (1, 0)", updated, rotated)
	}

	// Persisted in the gateway namespace.
	sec, err := cs.CoreV1().Secrets("agent-gateway").Get(ctx, sentinelKeySecretName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("sentinel key secret not stored: %v", err)
	}
	if !strings.Contains(string(sec.Data[sentinelKeysField]), "SENTINELKEY") {
		t.Errorf("stored sentinel key = %q", sec.Data[sentinelKeysField])
	}

	// The Pipe's from-keys now contain BOTH the agent and the sentinel key.
	from := pipeFromKeys(t, b, "alice")
	if !strings.Contains(from, "AGENTKEY") {
		t.Errorf("pipe from-keys lost the agent key: %q", from)
	}
	if !strings.Contains(from, "SENTINELKEY") {
		t.Errorf("pipe from-keys missing the sentinel key: %q", from)
	}
