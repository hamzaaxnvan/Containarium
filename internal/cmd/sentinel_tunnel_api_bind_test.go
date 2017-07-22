//go:build !windows && !containarium_client

package cmd

import (
	"testing"

	"github.com/footprintai/containarium/internal/sentinel"
)

func TestApplyTunnelAPIBindAddr(t *testing.T) {
	for _, tc := range []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "127.0.0.1", want: "127.0.0.1"},
