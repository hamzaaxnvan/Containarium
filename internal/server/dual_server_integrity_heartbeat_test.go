//go:build !containarium_client

package server

import (
	"testing"
	"time"
)

func TestResolveIntegrityHeartbeatInterval(t *testing.T) {
	tests := []struct {
		name         string
		raw          string
		wantInterval time.Duration
		wantInvalid  bool
		wantClamped  bool
	}{
		{
			name:         "environment variable unset uses default",
			wantInterval: defaultIntegrityHeartbeatInterval,
		},
		{
