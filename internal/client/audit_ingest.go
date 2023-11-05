package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// #2415 — typed client for AuditService.IngestSSHSessionRecords, on both
// transports. Request and response are the generated messages; the REST
