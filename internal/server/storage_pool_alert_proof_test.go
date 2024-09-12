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
