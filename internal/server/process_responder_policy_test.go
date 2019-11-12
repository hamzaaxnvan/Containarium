package server

import (
	"context"
	"sync"
	"testing"

	"github.com/footprintai/containarium/internal/app"
	"github.com/footprintai/containarium/internal/threatdetect"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// countingPolicyConfig is an in-memory PolicyConfigStore that counts reads.
type countingPolicyConfig struct {
	mu   sync.Mutex
	kv   map[string]string
	gets int
}

func (c *countingPolicyConfig) Get(_ context.Context, key string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gets++
	v, ok := c.kv[key]
	if !ok {
		return "", app.ErrDaemonConfigNotFound
	}
	return v, nil
}

func (c *countingPolicyConfig) Set(_ context.Context, key, value string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.kv[key] = value
	return nil
}

func (c *countingPolicyConfig) reads() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gets
}

// TestProcessResponder_ReadsPolicyOncePerFinding (#2768 item 5): the kill
// step and the policy action share one resolution of the effective policy.
// One Effective is two config reads (backend default, box override).
func TestProcessResponder_ReadsPolicyOncePerFinding(t *testing.T) {
	for _, action := range []pb.ProcessAction{pb.ProcessAction_PROCESS_ACTION_ALERT, pb.ProcessAction_PROCESS_ACTION_THROTTLE} {
		t.Run(action.String(), func(t *testing.T) {
			fx := newResponderFixture(t, false)
			cfg := &countingPolicyConfig{kv: map[string]string{}}
			fx.policies = threatdetect.NewProcessPolicyStore(cfg)
			fx.r.policies = fx.policies
			fx.setDefault(t, action, "", 0)
			fx.setKill(t, "", true)
			k := &fakeKiller{}
