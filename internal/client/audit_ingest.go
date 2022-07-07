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
// path speaks protojson, exactly what grpc-gateway expects and emits.

// IngestHTTPError is returned by the REST transport for a non-2xx
// response. It carries the status so the sentinel's shipper can tell a
// rejected batch (4xx: a contract bug, never retried) from an unavailable
// backend (5xx / transport error: retried).
type IngestHTTPError struct {
	StatusCode int
	Body       string
}

func (e *IngestHTTPError) Error() string {
	return fmt.Sprintf("ingest ssh session records: HTTP %d: %s", e.StatusCode, e.Body)
}

// IngestSSHSessionRecords appends a batch of sentinel SSH session records to
// the backend's audit chain over REST.
func (c *HTTPClient) IngestSSHSessionRecords(ctx context.Context, req *pb.IngestSSHSessionRecordsRequest) (*pb.IngestSSHSessionRecordsResponse, error) {
