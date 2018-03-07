package server

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/guardrailrate"
)

// Rate limits on the guardrail APIs (#2404). Each is per authenticated
// subject; a nil limiter is disabled.

// GuardrailRateLimits is the daemon's configuration for them (flags
// --guardrail-*-rate-per-minute / --guardrail-*-burst). A zero Limit is
// disabled.
