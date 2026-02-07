package server

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/agentengine"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// fakeInBox is the inBoxRunner seam: the in-box agent-runtime exec and the
// artifact.json read, scripted per test.
type fakeInBox struct {
	stdout, stderr string
	exitCode       int
	execErr        error
	artifact       []byte // nil = artifact.json absent
	readErr        error
	reads          int
}

func (f *fakeInBox) ExecWithExitCode(_ string, _ []string) (string, string, int, error) {
	return f.stdout, f.stderr, f.exitCode, f.execErr
}

func (f *fakeInBox) ReadFile(_, _ string) ([]byte, error) {
	f.reads++
	if f.readErr != nil {
		return nil, f.readErr
	}
	if f.artifact == nil {
		return nil, errors.New("not found")
	}
	return f.artifact, nil
}

func runInBoxWith(t *testing.T, box *fakeInBox) (string, error) {
	t.Helper()
	s, skill := newSkillBoxHarness(t, newFakeRevocationStore())
	s.inBoxOps = box
	return s.runInBoxAgentResult("agent-box", "/seed", "run-1", skill.Id, agentengine.Resolved{}, "")
}

// TestRunInBoxAgentResult_NonZeroExit covers the in-box failure paths: the
// engine's own error recorded in artifact.json must reach the caller, a
// missing runtime is told apart from a failed run, and a failed run with no
// artifact is still an error.
func TestRunInBoxAgentResult_NonZeroExit(t *testing.T) {
	tests := []struct {
		name         string
		box          *fakeInBox
		wantContains []string
		wantMissing  bool // errors.Is(err, errInBoxRuntimeMissing)
	}{
		{
			name: "exit 1 surfaces the artifact error",
			box: &fakeInBox{exitCode: 1, stderr: "agent-runtime: claude run failed",
				artifact: []byte(`{"outputJson":"","error":"upstream 401: API key not valid"}`)},
			wantContains: []string{"upstream 401: API key not valid", "exited 1"},
		},
		{
			name:         "exit 1 with no artifact still errors",
			box:          &fakeInBox{exitCode: 1},
			wantContains: []string{"exited 1"},
		},
		{
			name:         "exit 1 with an unparseable artifact still errors",
			box:          &fakeInBox{exitCode: 1, artifact: []byte("not json")},
			wantContains: []string{"exited 1"},
		},
		{
			name:         "exit 2 (missing run/skill id) surfaces the artifact error",
			box:          &fakeInBox{exitCode: 2, artifact: []byte(`{"error":"CONTAINARIUM_RUN_ID is not set"}`)},
			wantContains: []string{"CONTAINARIUM_RUN_ID is not set", "exited 2"},
