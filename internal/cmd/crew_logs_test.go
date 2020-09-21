package cmd

import (
	"bytes"
	"context"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/spf13/cobra"
)

// fakeJournalServer serves a journal the way TailRunLog does, three chunks
// then ended, and records every request.
type fakeJournalServer struct {
	journal []byte
	cuts    []int64 // end offsets of successive windows
	reqs    []*pb.TailRunLogRequest
}

func (f *fakeJournalServer) TailRunLog(req *pb.TailRunLogRequest) (*pb.TailRunLogResponse, error) {
	f.reqs = append(f.reqs, req)
	start := req.GetStartOffset()
	end := start
	for _, c := range f.cuts {
		if c > start {
			end = c
			break
		}
	}
	return &pb.TailRunLogResponse{
		Chunk:     f.journal[start:end],
		EndOffset: end,
		Ended:     end == int64(len(f.journal)),
	}, nil
}

func TestCrewLogsFollow_ByteExact(t *testing.T) {
	journal := []byte(`{"seq":1,"kind":"status","text":"run started"}` + "\n" +
		`{"seq":2,"kind":"assistant","text":"héllo"}` + "\n" +
		`{"seq":3,"kind":"status","text":"run ended exit=0"}` + "\n")
	// The second cut splits a line (and a multi-byte rune): the output must
	// still be the journal, byte for byte.
	f := &fakeJournalServer{journal: journal, cuts: []int64{20, 66, int64(len(journal))}}
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	var out bytes.Buffer
	if err := crewLogs(cmd, f, "run-1", "hello-agent", true, &out); err != nil {
		t.Fatal(err)
	}
