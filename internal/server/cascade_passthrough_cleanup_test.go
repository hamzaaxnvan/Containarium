package server

import (
	"context"
	"testing"

	"github.com/footprintai/containarium/pkg/core/network"
)

// #1462: a recipe-deployed box's TCP/UDP passthrough routes must be removed
// on delete, the same way its HTTP routes already are — otherwise a deleted
// box's external_port stays claimed forever, and a later recipe deploy
// wanting the same port fails with a stale "already claimed" conflict.
func TestCascadeContainerCleanup_RemovesOwnedPassthroughRoutes(t *testing.T) {
	store := newFakePassthroughStore()
