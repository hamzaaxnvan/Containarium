package server

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/footprintai/containarium/internal/poolcapacity"
	"github.com/footprintai/containarium/pkg/core/incus"
)

// #3129: prove the pool-fill alert fires, with no live backend. There is no
// PromQL engine in the module, so these tests evaluate the one expression
// shape the rule uses ("<used> / <total> * 100 >= N") against a synthetic
// series and apply the rule's `for` window the way vmalert does. An
// expression that stops matching that shape fails the parse, so a rewritten
// rule cannot slip past unevaluated.

var poolFillExpr = regexp.MustCompile(`^\s*(\w+)\s*/\s*(\w+)\s*\*\s*100\s*>=\s*(\d+)\s*$`)

type poolRule struct {
	used, total string
	threshold   float64
	forWindow   time.Duration
	severity    string
}

func loadPoolRule(t *testing.T, name string) poolRule {
	t.Helper()
	rules, err := parseDefaultAlertRules()
	if err != nil {
		t.Fatalf("DefaultAlertRules does not parse: %v", err)
	}
	for _, r := range rules {
		if r.GetName() != name {
			continue
		}
		m := poolFillExpr.FindStringSubmatch(r.GetExpr())
		if m == nil {
			t.Fatalf("%s expr %q is not the evaluable shape 'used / total * 100 >= N'", name, r.GetExpr())
		}
		th, _ := strconv.ParseFloat(m[3], 64)
		d, err := time.ParseDuration(r.GetDuration())
		if err != nil {
			t.Fatalf("%s for=%q: %v", name, r.GetDuration(), err)
		}
		return poolRule{used: m[1], total: m[2], threshold: th, forWindow: d, severity: r.GetSeverity()}
	}
	t.Fatalf("rule %s not found in DefaultAlertRules", name)
	return poolRule{}
}

type alertState int

const (
	inactive alertState = iota
	pending
	firing
)

// eval walks one series (a sample per step) and returns the state after each
// sample, following Prometheus semantics: pending from the first sample that
// satisfies the condition, firing once it has held for the `for` window, reset
// by any sample that does not satisfy it.
func (r poolRule) eval(usedBytes []float64, total float64, step time.Duration) []alertState {
	out := make([]alertState, len(usedBytes))
	var since time.Duration
	active := false
	for i, u := range usedBytes {
		at := time.Duration(i) * step
		if u/total*100 >= r.threshold {
			if !active {
				active, since = true, at
			}
			if at-since >= r.forWindow {
