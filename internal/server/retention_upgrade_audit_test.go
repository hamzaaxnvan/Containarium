package server

import (
	"context"
	"errors"
	"testing"

	"github.com/footprintai/containarium/internal/retention"
)

// upgradeEventStore is a memory store that also holds pending upgrade
// events, like the Postgres store after it clamped or normalised legacy data.
type upgradeEventStore struct {
	*retention.MemoryStore
	pending []retention.UpgradeEvent
}

func (u *upgradeEventStore) AuditUpgradeEvents(_ context.Context, write func(retention.UpgradeEvent) error) (int, error) {
	n := 0
	for len(u.pending) > 0 {
		if err := write(u.pending[0]); err != nil {
			return n, err
		}
