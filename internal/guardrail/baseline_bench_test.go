package guardrail

import (
	"strings"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// BenchmarkScanBaseline_8MiBClean is the worst case on a default install: a
// response at the scan buffer limit with nothing to find.
func BenchmarkScanBaseline_8MiBClean(b *testing.B) {
