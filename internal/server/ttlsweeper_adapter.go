package server

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/ttlsweeper"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// ttlsweeperIncusAdapter bridges *incus.Client → ttlsweeper.IncusClient.
//
// The ttlsweeper package was deliberately decoupled from
// pkg/core/incus and pkg/pb to keep its decision logic pure-Go and
// trivially testable; the wiring lives here on the daemon side, where
// the binding to the real Incus client costs ~15 lines and keeps the
// dependency arrow pointing the right way (daemon → sweeper, never
// sweeper → daemon).
//
// The adapter strips ListContainers down to just (Name, TTLExpiresAt)
// — the only fields Decide actually consumes — and skips core
// containers entirely. Core containers must never carry a TTL; this
// is defense in depth in case a writer somewhere stamps the key by
// mistake. Decide would still skip them (because no one should be
// setting the key in the first place), but excluding here means a
// stray key never even reaches the decision function.
type ttlsweeperIncusAdapter struct {
	ic *incus.Client
}

// ListContainers returns one ContainerView per non-core container
// the Incus client knows about. Containers whose TTL key parses as
// the zero time (missing/empty/malformed) carry a nil TTLExpiresAt
// so Decide's "skip" branch is naturally exercised.
func (a *ttlsweeperIncusAdapter) ListContainers() ([]ttlsweeper.ContainerView, error) {
	raw, err := a.ic.ListContainers()
	if err != nil {
		return nil, err
	}
	out := make([]ttlsweeper.ContainerView, 0, len(raw))
	for i := range raw {
		c := raw[i]
		if c.Role.IsCoreRole() {
			continue
		}
		v := ttlsweeper.ContainerView{Name: c.Name}
		if !c.TTLExpiresAt.IsZero() {
			t := c.TTLExpiresAt
			v.TTLExpiresAt = &t
		}
		// Stopped→delete inputs (#525). Only a genuinely STOPPED box with a
		// recorded stop time and an opted-in window is eligible; everything
		// else leaves these nil/false and Decide skips the stopped rule.
		v.Stopped = strings.EqualFold(c.State, "Stopped")
		if !c.StoppedAt.IsZero() {
			s := c.StoppedAt
			v.StoppedAt = &s
		}
		if c.DeleteAfterStoppedSeconds > 0 {
