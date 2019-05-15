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
