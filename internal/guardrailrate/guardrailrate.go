// Package guardrailrate is the per-subject token-bucket rate limit on the
// guardrail APIs (#2404): the gated DeployRecipe path and
// GuardrailPolicyService Set/Get. There is no limit on how much data a gated
// deploy may copy (#2368); what is bounded is how often a caller may ask the
// daemon to do that work.
//
// The model is internal/sandbox/ratelimit (a golang.org/x/time/rate bucket
// per subject, burst defaulting to the per-minute rate), plus what a
// guardrail limit needs on top: an injectable clock, a gRPC
// RESOURCE_EXHAUSTED refusal carrying a RetryInfo hint (HTTP 429 through
// grpc-gateway), one throttle log line per caller per ThrottleLogInterval,
// and eviction of idle buckets.
package guardrailrate

import (
	"fmt"
	"log"
	"math"
	"sync"
	"time"

	"golang.org/x/time/rate"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
)

// ThrottleLogInterval is the most often one caller's throttling is logged
// per limit; a flood of refused calls cannot flood the daemon log.
const ThrottleLogInterval = time.Minute

// Limit is one configured rate. PerMinute <= 0 disables the limit. Burst <= 0
// defaults to PerMinute rounded down, floor 1 (internal/sandbox/ratelimit's
