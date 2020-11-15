//go:build !containarium_client

package server

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/agentbox"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

func TestStructuredSessionID(t *testing.T) {
	cases := []struct {
		name string
		log  string
		want string
	}{
		{"plain", `{"type":"result","session_id":"good-1"}`, "good-1"},
		{"nested key earlier in byte order is ignored",
			`{"permission_denials":[{"tool_input":{"session_id":"evil"}}],"session_id":"good"}`, "good"},
		{"nested only", `{"permission_denials":[{"session_id":"evil"}]}`, ""},
		{"escaped quotes inside result text",
			`{"result":"he said \"session_id\":\"evil\" ok","session_id":"good"}`, "good"},
		{"truncated", `{"type":"result","session_id":"good-1"`, ""},
		{"invalid", `not json`, ""},
		{"empty", ``, ""},
		{"top-level array", `[{"session_id":"x"}]`, ""},
		{"non-string id", `{"session_id":12}`, ""},
		{"unsafe id", `{"session_id":"../etc"}`, ""},
		{"trailing garbage", `{"session_id":"good"} {"x":1}`, ""},
	}
	for _, tc := range cases {
		if got := structuredSessionID([]byte(tc.log)); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestParseBoxRunList_StructuredResultOverridesSidecar feeds the parser a
// list line carrying a result log: the decoded top-level id wins over the
