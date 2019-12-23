package modelgateway

import (
	"encoding/json"
	"strings"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Scan-unit extraction for the inbound guardrail (#2367 slice B2). Every
// fixture is synthetic and in the real provider wire shape. The property
// under test: every text part and every tool call's name plus argument
// string becomes a unit, arguments split across streamed chunks are
// reassembled, and a shape nothing recognises is scanned whole rather than
// not at all.

func unitMap(units []*pb.GuardrailTextUnit) map[string]string {
	m := map[string]string{}
	for _, u := range units {
		m[u.GetUnitId()] = u.GetText()
	}
	return m
}

func TestInboundUnitsJSON(t *testing.T) {
	cases := []struct {
		name string
		body string
		want map[string]string
	}{
