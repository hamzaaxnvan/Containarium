package modelgateway

import (
	"strings"
	"testing"
	"time"
)

func TestQuotaLimits_IsZeroAndNormalize(t *testing.T) {
	if !(QuotaLimits{}).IsZero() {
		t.Error("empty limits should report IsZero")
	}
	// A window alone is not a limit: it says when to measure, not how much.
	if !(QuotaLimits{Window: time.Hour}).IsZero() {
		t.Error("a window with no caps should still report IsZero")
	}
	if (QuotaLimits{MaxCalls: 1}).IsZero() {
		t.Error("a call cap is a limit")
	}
	if got := (QuotaLimits{}).normalized().Window; got != time.Minute {
		t.Errorf("default window = %v, want 1m", got)
	}
}

// The fraction is the MAX across dimensions, not an average: a tenant that has
// burned all its output-token budget in three calls is out of budget, and
// averaging that against its untouched call quota would hide it.
func TestQuotaLimits_FractionIsWorstDimension(t *testing.T) {
	q := QuotaLimits{Window: time.Minute, MaxCalls: 100, MaxTotalTokens: 1000, MaxOutputTokens: 10}

	u := q.evaluate(counts{calls: 3, inputTokens: 5, outputTokens: 9})
	if u.exceeded != "" {
		t.Errorf("exceeded = %q, want none", u.exceeded)
	}
	if want := 0.9; u.fraction != want {
		t.Errorf("fraction = %v, want %v (output tokens are the worst dimension)", u.fraction, want)
	}
}

func TestQuotaLimits_ExceededNamesTheDimension(t *testing.T) {
	q := QuotaLimits{Window: time.Minute, MaxCalls: 2, MaxTotalTokens: 1000}

	u := q.evaluate(counts{calls: 2, inputTokens: 1})
	if u.exceeded != "calls" {
		t.Errorf("exceeded = %q, want calls (at the limit counts as exceeded)", u.exceeded)
	}
	if d := q.describe(u); !strings.Contains(d, "calls 2/2") {
		t.Errorf("describe = %q, want it to name the consumed dimension", d)
	}

	u = q.evaluate(counts{calls: 1, inputTokens: 600, outputTokens: 500})
	if u.exceeded != "total_tokens" {
		t.Errorf("exceeded = %q, want total_tokens", u.exceeded)
	}
}

// An unset dimension is unlimited, so it must not contribute a fraction — a
// zero limit divided into any usage would otherwise be an instant overrun.
func TestQuotaLimits_UnsetDimensionsAreUnlimited(t *testing.T) {
	q := QuotaLimits{Window: time.Minute, MaxTotalTokens: 100}

	// A million calls with an uncapped call limit, and token spend well under
	// the one cap that is set.
	u := q.evaluate(counts{calls: 1_000_000, inputTokens: 10, outputTokens: 20})
	if u.exceeded != "" {
		t.Errorf("exceeded = %q, want none: only total_tokens is capped and it is under", u.exceeded)
	}
	if u.fraction > 1 {
		t.Errorf("fraction = %v, want <= 1", u.fraction)
	}
}

// Cached tokens count toward spend: replaying a huge cached prefix is exactly
// the runaway a budget exists to stop.
func TestQuotaLimits_CachedTokensCountTowardTotal(t *testing.T) {
	q := QuotaLimits{Window: time.Minute, MaxTotalTokens: 100}
	if u := q.evaluate(counts{calls: 1, cachedTokens: 150}); u.exceeded != "total_tokens" {
		t.Errorf("exceeded = %q, want total_tokens", u.exceeded)
	}
}

// Denials of either kind are not spend — they never reached a provider, so they
// must not consume the tenant's budget. A throttled tenant whose refusals ate
// its own quota could never climb back out.
func TestQuotaLimits_DenialsAreNotSpend(t *testing.T) {
	q := QuotaLimits{Window: time.Minute, MaxCalls: 5, MaxTotalTokens: 100}
	if u := q.evaluate(counts{authDenials: 50, policyDenials: 50}); u.exceeded != "" || u.fraction != 0 {
		t.Errorf("denials consumed budget: exceeded=%q fraction=%v", u.exceeded, u.fraction)
	}
}
