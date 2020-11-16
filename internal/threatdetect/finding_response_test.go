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
	t0 := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	in := FindingResponse{
		Action:     pb.ProcessAction_PROCESS_ACTION_THROTTLE,
		State:      pb.ResponseState_RESPONSE_STATE_APPLIED,
		AppliedAt:  t0,
		ExpiresAt:  t0.Add(24 * time.Hour),
		ReleasedBy: "",
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, want := range []string{`"PROCESS_ACTION_THROTTLE"`, `"RESPONSE_STATE_APPLIED"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("JSON %s lacks %s", raw, want)
		}
