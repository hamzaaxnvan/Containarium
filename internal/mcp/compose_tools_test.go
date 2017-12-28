package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// Per-workstream test file (NEW; not appended to a shared *_test.go).
// Drives the compose-tool handlers without a real daemon — uses a
// stubbed *Client whose doRequest is wired to an in-memory response.
//
// We don't try to run the typed `client.composeDispatch` path here
// since that's a thin wrapper over doRequest (already covered by the
// daemon-side tests in internal/server/compose_autostart_server_test.go).
// The tests below focus on the args→request mapping and the
// schema-required validation the handlers perform locally.

func TestComposeTools_RegisteredFour(t *testing.T) {
	tools := composeTools()
	if len(tools) != 4 {
		t.Fatalf("composeTools() returned %d, want 4", len(tools))
	}
	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
		if tool.Handler == nil {
			t.Errorf("tool %q has nil Handler", tool.Name)
		}
		if tool.Description == "" {
			t.Errorf("tool %q has empty Description", tool.Name)
		}
		if tool.InputSchema == nil {
			t.Errorf("tool %q has nil InputSchema", tool.Name)
		}
	}
	for _, want := range []string{"compose_discover", "compose_enable", "compose_disable", "compose_status"} {
		if !names[want] {
			t.Errorf("missing tool: %q", want)
		}
	}
}

func TestComposeTools_RequiredFields(t *testing.T) {
	cases := []struct {
		name   string
		schema map[string]interface{}
		want   []string
	}{
		{"compose_discover", nil, []string{"username"}},
		{"compose_enable", nil, []string{"username", "dir"}},
		{"compose_disable", nil, []string{"username", "dir"}},
		{"compose_status", nil, []string{"username", "dir"}},
	}
	tools := composeTools()
	byName := map[string]Tool{}
	for _, t2 := range tools {
		byName[t2.Name] = t2
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			tool, ok := byName[tc.name]
			if !ok {
				t.Fatalf("tool %q not found", tc.name)
			}
			req, _ := tool.InputSchema["required"].([]string)
			if len(req) != len(tc.want) {
				t.Errorf("required = %v, want %v", req, tc.want)
				return
			}
			for i, r := range req {
				if r != tc.want[i] {
					t.Errorf("required[%d] = %q, want %q", i, r, tc.want[i])
				}
			}
		})
	}
}

// handler dispatch — verify each handler exists and respects local
