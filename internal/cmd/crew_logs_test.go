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
