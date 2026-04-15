package threatdetect

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// FindingResponse is stored as JSONB on security_findings; it must round-trip
// with enum names (not bare ints) so the column is readable in psql and
// stable if the enum grows.
func TestFindingResponse_JSONRoundTrip(t *testing.T) {
