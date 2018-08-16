package server

import (
	"context"
	"errors"
	"log"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/proofpack"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

const (
	// maxVerifyPackBytes bounds the encoded sealed pack VerifyProofPack
	// accepts. The pack is client-supplied, so it is bounded here as well as
	// by the transport's own receive limit.
	maxVerifyPackBytes = 4 << 20
	// verifyTimeout bounds the chain walk, which is linear in the rows
	// between the seal row and the tip.
	verifyTimeout = 2 * time.Minute
	// maxConcurrentVerifies bounds the chain walks running at once; a caller
	// over the cap is refused, not queued.
	maxConcurrentVerifies = 4
)

// The messages a response carries, one per verdict. The audit chain is
// global, so a response never says which row, base id, date or underlying
