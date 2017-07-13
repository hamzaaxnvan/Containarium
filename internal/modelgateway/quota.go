package modelgateway

import (
	"fmt"
	"time"
)

// Per-tenant spend budgets enforced at the gateway (design note responsibility
// #7 — "one tenant's runaway agent can't exhaust the shared account").
//
// The gateway is the only place this can be enforced honestly. A limit applied
// in the box is advice: the box holds the credential and can ignore it. Here the
// tenant cannot spend without asking us first.
//
// Mechanism only, per the OSS/Cloud split: this defines budgets and windows, not
// named tiers or per-model pricing. Cloud sets the numbers.

// QuotaLimits caps what one tenant may consume within a rolling window. A zero
// field means that dimension is unlimited, so a partially-filled QuotaLimits is
// meaningful — capping tokens while leaving call count free is a normal
// configuration, not an error.
type QuotaLimits struct {
	// Window is the rolling period the limits apply over. Zero defaults to a
	// minute at construction.
	Window time.Duration `json:"window"`

	// MaxCalls caps requests per window — the rate-limit dimension. Guards the
	// shared account's provider request-rate limit, which a tenant can exhaust
	// with cheap calls without spending many tokens.
	MaxCalls int64 `json:"max_calls"`

	// MaxTotalTokens caps input+output+cached tokens per window — the spend
	// dimension, and the one that maps to money.
	MaxTotalTokens int64 `json:"max_total_tokens"`

	// MaxOutputTokens caps generated tokens per window. Separate because output
	// is the expensive half on every provider, so a deployment may want a
	// tighter ceiling on it than on total.
	MaxOutputTokens int64 `json:"max_output_tokens"`
}

// IsZero reports whether no dimension is limited — a tenant with no budget
// configured, which the policy treats as "observe only".
func (q QuotaLimits) IsZero() bool {
	return q.MaxCalls == 0 && q.MaxTotalTokens == 0 && q.MaxOutputTokens == 0
}

func (q QuotaLimits) normalized() QuotaLimits {
	if q.Window <= 0 {
		q.Window = time.Minute
	}
	return q
}
