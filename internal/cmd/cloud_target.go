package cmd

import (
	"errors"
	"fmt"

	"github.com/footprintai/containarium/internal/credentials"
)

// isCloudTarget reports whether the effective (server, token) points at the
// hosted control plane, where host-level operations — system info, per-box
// debug, control-plane upgrade / release checks — are not available per
// tenant. Host-level CLI commands call this to refuse CLIENT-SIDE with a clear
// message instead of round-tripping to an opaque 404.
//
// Two signals, mirroring the MCP backend classifier (#456):
