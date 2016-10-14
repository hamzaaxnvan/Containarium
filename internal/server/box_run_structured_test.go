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
// literal sidecar; an undecodable log leaves the sidecar alone.
func TestParseBoxRunList_StructuredResultOverridesSidecar(t *testing.T) {
	rec, err := json.Marshal(agentbox.RunRecord{Version: agentbox.RunRecordVersion, Name: "s", PID: 1, StartedAt: time.Now().UTC(), CaptureMode: agentbox.CaptureCombined})
	if err != nil {
		t.Fatal(err)
	}
	b := func(s string) string {
		if s == "" {
			return "-"
		}
		return base64.StdEncoding.EncodeToString([]byte(s))
	}
	line := func(sid, result string) string {
		return fmt.Sprintf("B boot\nP \nR s.json %s - %s %s -\n", b(string(rec)), b(sid), b(result))
	}
	nested := `{"permission_denials":[{"tool_input":{"session_id":"evil"}}],"session_id":"good"}`
	if got := parseBoxRunList(line("resumed-id", nested), "/d")[0].GetSessionId(); got != "good" {
		t.Errorf("session_id = %q, want good", got)
	}
	if got := parseBoxRunList(line("resumed-id", `{"session_id":"good"`), "/d")[0].GetSessionId(); got != "resumed-id" {
		t.Errorf("truncated result: session_id = %q, want the sidecar's", got)
	}
	if got := parseBoxRunList(line("", `{"x":1}`), "/d")[0].GetSessionId(); got != "" {
		t.Errorf("no id anywhere: session_id = %q, want empty", got)
	}
}

func TestBoxRunNameCollidesWithDerivedFile(t *testing.T) {
	for name, want := range map[string]bool{
		"job": false, "job.v2": false, "job-1": false, "a.b.c": false, "run.": false,
		"job.stderr": true, "job.1700000000": true, "x.0": true, "stderr": false,
	} {
		if got := boxRunNameCollidesWithDerivedFile(name); got != want {
			t.Errorf("%q: got %v, want %v", name, got, want)
		}
	}
}

func TestStartBoxRun_DerivedFileRunNamesAreRefusedBeforeAnyExec(t *testing.T) {
	for _, bad := range []string{"a.stderr", "a.1700000000", "a.7"} {
		var execCalls int
		s := &ContainerServer{
			boxRunExec:      func(box, script string) (string, error) { execCalls++; return "", nil },
			boxRunStartExec: func(box, username, script string) (string, error) { execCalls++; return "", nil },
		}
		_, err := s.StartBoxRun(adminCtx(), &pb.StartBoxRunRequest{Username: "alice", Prompt: "hi", RunName: bad})
		if status.Code(err) != codes.InvalidArgument || execCalls != 0 {
			t.Errorf("run_name %q: err = %v, execs = %d", bad, err, execCalls)
		}
	}
}

