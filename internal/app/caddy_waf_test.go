package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCaddyWAFHandler_JSON(t *testing.T) {
	h := CaddyWAFHandler{Handler: "waf", Directives: "SecRuleEngine On"}
	if h.HandlerName() != "waf" {
		t.Fatalf("HandlerName = %q, want waf", h.HandlerName())
	}
	b, err := json.Marshal(h)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(b)
	if !strings.Contains(got, `"handler":"waf"`) || !strings.Contains(got, `"directives":"SecRuleEngine On"`) {
		t.Fatalf("WAF handler JSON wrong: %s", got)
	}
}

func TestDefaultWAFDirectives(t *testing.T) {
	on := DefaultWAFDirectives(true)
	if !strings.Contains(on, "SecRuleEngine On") {
		t.Errorf("enforce should set SecRuleEngine On: %q", on)
	}
	off := DefaultWAFDirectives(false)
	if !strings.Contains(off, "SecRuleEngine DetectionOnly") {
		t.Errorf("non-enforce should set DetectionOnly: %q", off)
	}
	// Both load the CRS.
	for _, d := range []string{on, off} {
