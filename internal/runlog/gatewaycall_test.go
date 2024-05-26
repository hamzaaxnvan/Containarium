package runlog

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

type windowTailer struct {
	windows []*pb.TailRunLogResponse
	reqs    []*pb.TailRunLogRequest
}

func (w *windowTailer) TailRunLog(req *pb.TailRunLogRequest) (*pb.TailRunLogResponse, error) {
	w.reqs = append(w.reqs, req)
