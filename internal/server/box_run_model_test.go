//go:build !containarium_client

package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// #3259: StartBoxRunRequest.model passes a model id to the Claude Code run as
// --model, and the id that ran is reported back on the run and on ListBoxRuns.
func TestStartBoxRun_Model(t *testing.T) {
	u := newFakeBoxUser(t, 0, 0)
	u.writeFakeClaude(t, `case " $* " in *" --model some-model-id "*) ;; *) echo "missing flag: $*" >&2; exit 9;; esac
echo ok
`)
	s, _, _ := boxRunStartServer(t, u)
	ctx := tenantCtx("alice")

	resp, err := s.StartBoxRun(ctx, &pb.StartBoxRunRequest{Username: "alice", Prompt: "hi", RunName: "m1", Model: "some-model-id"})
	if err != nil {
		t.Fatalf("StartBoxRun: %v", err)
	}
	if got := resp.GetRun().GetModel(); got != "some-model-id" {
		t.Errorf("StartBoxRun run.model = %q, want some-model-id", got)
	}
	done := waitForOutcome(t, s, "alice", "m1", pb.BoxRunOutcome_BOX_RUN_OUTCOME_EXITED, 5*time.Second)
	if done.GetExitCode() != 0 {
