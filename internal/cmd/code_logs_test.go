package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// fakeBoxRunServer serves a run's log the way TailBoxRunLog does, in
// windows ending at cuts, and records every request.
type fakeBoxRunServer struct {
	log           []byte
	cuts          []int64
	stderrDropped bool
	reqs          []*pb.TailBoxRunLogRequest
	runs          []*pb.BoxRun
	listed        []string
}

func (f *fakeBoxRunServer) TailBoxRunLog(req *pb.TailBoxRunLogRequest) (*pb.TailBoxRunLogResponse, error) {
	f.reqs = append(f.reqs, req)
	start := req.GetStartOffset()
	end := start
	for _, c := range f.cuts {
		if c > start {
			end = c
			break
		}
	}
	return &pb.TailBoxRunLogResponse{
		Chunk:         f.log[start:end],
		EndOffset:     end,
		Ended:         end == int64(len(f.log)),
		StderrDropped: f.stderrDropped,
	}, nil
}

func (f *fakeBoxRunServer) ListBoxRuns(username string) ([]*pb.BoxRun, error) {
	f.listed = append(f.listed, username)
	return f.runs, nil
}

// `code logs --follow` is byte-exact over three windows, the same harness
// `crew logs` is held to (#2096).
func TestCodeLogsFollow_ByteExact(t *testing.T) {
	log := []byte("building...\nhéllo from the agent\ndone\n")
	// The second cut splits a multi-byte rune: output is still the log, byte
	// for byte.
	f := &fakeBoxRunServer{log: log, cuts: []int64{7, 14, int64(len(log))}}
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	var out, diag bytes.Buffer
	if err := codeLogs(cmd, f, "alice", "task", true, &out, &diag); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), log) {
		t.Errorf("output =\n%q\nwant\n%q", out.Bytes(), log)
	}
	if len(f.reqs) != 3 {
		t.Fatalf("%d requests, want 3 (stop at ended)", len(f.reqs))
	}
	for i, want := range []int64{0, 7, 14} {
		r := f.reqs[i]
		if r.GetStartOffset() != want || r.GetUsername() != "alice" || r.GetRunName() != "task" || r.GetFollowSeconds() == 0 {
			t.Errorf("request %d = %+v, want start_offset %d, follow set", i, r, want)
