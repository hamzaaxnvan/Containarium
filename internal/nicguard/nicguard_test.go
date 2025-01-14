package nicguard

import (
	"errors"
	"net/netip"
	"testing"

	"github.com/lxc/incus/v7/shared/api"

	"github.com/footprintai/containarium/pkg/core/incus"
)

// Unset arms the guard (a boundary, not optional hardening); only an
// explicit off-word disables, and a typo fails closed.
func TestParseMode(t *testing.T) {
	tests := []struct {
		in   string
		want Mode
	}{
		{"", ModeEnforce},
		{"enforce", ModeEnforce},
		{"Enforce", ModeEnforce},
		{" enforce ", ModeEnforce},
		{"1", ModeEnforce},
		{"true", ModeEnforce},
		{"typo", ModeEnforce},
		{"off", ModeOff},
		{"OFF", ModeOff},
		{" off ", ModeOff},
		{"0", ModeOff},
		{"false", ModeOff},
