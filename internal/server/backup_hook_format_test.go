package server

import (
	"testing"

	"github.com/footprintai/containarium/pkg/core/backup"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// #2405: the wire enum and the core's typed format map one-to-one, and an
// unset request format means opaque (today's behaviour), never pg_custom.
func TestHookFormatFromProto(t *testing.T) {
