package anonbox

import (
	"context"
	"strings"
	"testing"
	"time"
)

type recFunnel struct{ events []FunnelEvent }

func (r *recFunnel) Record(e FunnelEvent) { r.events = append(r.events, e) }

func (r *recFunnel) kinds() string {
	out := make([]string, 0, len(r.events))
	for _, e := range r.events {
		out = append(out, string(e.Kind))
	}
	return strings.Join(out, ",")
}

func funnelHarness(t *testing.T, mut func(*Config)) (*harness, *recFunnel) {
	t.Helper()
	f := &recFunnel{}
	h := newHarness(t, func(c *Config) {
		c.Funnel = f
		if mut != nil {
			mut(c)
		}
	})
	return h, f
}

// Every Ensure path records exactly the steps the design names, once.
func TestFunnel_EnsurePaths(t *testing.T) {
	t.Run("first create then reconnect", func(t *testing.T) {
		h, f := funnelHarness(t, nil)
		key, fp := testKey(t)
		req := EnsureRequest{Fingerprint: fp, PublicKey: key}
		// A clock that advances one second per read, so time-to-shell is > 0.
		cur := h.now
		h.m.cfg.Now = func() time.Time { t := cur; cur = cur.Add(time.Second); return t }
		if _, err := h.m.Ensure(context.Background(), req); err != nil {
			t.Fatal(err)
		}
		if got := f.kinds(); got != "connect,claim_link_issued,shell_ready" {
