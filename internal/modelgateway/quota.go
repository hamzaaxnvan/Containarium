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

// quotaUsage is one tenant's consumption against its budget, as of a moment.
type quotaUsage struct {
	// fraction is the most-consumed dimension's share of its limit: 0 for an
	// idle tenant, 1.0 at the limit, above 1.0 once exceeded. The MAX across
	// dimensions rather than an average — a tenant at 100% of output tokens is
	// out of budget regardless of how few calls it took to get there.
	fraction float64

	// exceeded names the first dimension found over its limit, empty when the
	// tenant is within budget. Named rather than boolean so the denial message
	// and the alert can say which budget ran out.
	exceeded string

	calls  int64
	tokens int64
	output int64
}

// evaluate scores c against the limits.
func (q QuotaLimits) evaluate(c counts) quotaUsage {
	u := quotaUsage{calls: c.calls, tokens: c.totalTokens(), output: c.outputTokens}
	consider := func(used, limit int64, name string) {
		if limit <= 0 {
			return
		}
		if f := float64(used) / float64(limit); f > u.fraction {
			u.fraction = f
		}
		if used >= limit && u.exceeded == "" {
			u.exceeded = name
		}
	}
	consider(c.calls, q.MaxCalls, "calls")
	consider(c.totalTokens(), q.MaxTotalTokens, "total_tokens")
	consider(c.outputTokens, q.MaxOutputTokens, "output_tokens")
	return u
}

// describe renders the budget state for a denial message or an alert. It reports
// consumption against the limits and never includes prompt or response content —
// a quota message is operational, and the gateway's whole purpose is to keep
// model traffic from leaking into places it shouldn't.
func (q QuotaLimits) describe(u quotaUsage) string {
	switch u.exceeded {
	case "calls":
		return fmt.Sprintf("calls %d/%d per %s", u.calls, q.MaxCalls, q.Window)
	case "total_tokens":
		return fmt.Sprintf("tokens %d/%d per %s", u.tokens, q.MaxTotalTokens, q.Window)
	case "output_tokens":
		return fmt.Sprintf("output tokens %d/%d per %s", u.output, q.MaxOutputTokens, q.Window)
	}
	return fmt.Sprintf("%.0f%% of budget per %s", u.fraction*100, q.Window)
}
