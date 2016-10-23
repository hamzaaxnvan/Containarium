package runlog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// The model gateway's per-call events (#2928) travel in TailRunLogResponse next
// to the journal bytes, because a gateway failure happens outside the box and
// is never written to the journal. Both carry a time, so a reader that prints
// the journal (Copy) places each event before the first journal line later than
// it. Whole lines only: a window can end mid-line, and nothing is ever written
// into the middle of one.

// gatewayCallLine is the JSON-lines rendering of one event in `crew logs`
// output. Typed fields, plus a plain-language summary built from them.
type gatewayCallLine struct {
	Kind           string `json:"kind"`
	Time           string `json:"time,omitempty"`
	Skill          string `json:"skill,omitempty"`
	Provider       string `json:"provider"`
	Model          string `json:"model,omitempty"`
	KeyOwner       string `json:"key_owner"`
	Status         int32  `json:"status,omitempty"`
	TransportError string `json:"transport_error,omitempty"`
	LatencyMs      int64  `json:"latency_ms"`
	Text           string `json:"text"`
}

// WriteGatewayCalls writes one JSON line per event to w.
func WriteGatewayCalls(w io.Writer, calls []*pb.GatewayCall) error {
	for _, c := range calls {
		l := gatewayCallLine{
			Kind: "gateway_call", Skill: c.GetSkillId(), Provider: c.GetProvider(), Model: c.GetModel(),
			KeyOwner: c.GetKeyOwner().String(), Status: c.GetStatus(), LatencyMs: c.GetLatencyMs(),
			Text: DescribeGatewayCall(c),
		}
		if c.GetTransportError() != pb.GatewayTransportError_GATEWAY_TRANSPORT_ERROR_UNSPECIFIED {
			l.TransportError = c.GetTransportError().String()
		}
		if c.GetTime() != nil {
			l.Time = c.GetTime().AsTime().UTC().Format(time.RFC3339)
		}
		b, err := json.Marshal(l)
		if err != nil {
			return err
		}
		if _, err := w.Write(append(b, '\n')); err != nil {
			return err
		}
	}
	return nil
}

// DescribeGatewayCall is the one-line reading of an event, derived from its
// typed fields only: "Model call refused: credential invalid (401)".
func DescribeGatewayCall(c *pb.GatewayCall) string {
	if te := c.GetTransportError(); te != pb.GatewayTransportError_GATEWAY_TRANSPORT_ERROR_UNSPECIFIED {
		switch te {
		case pb.GatewayTransportError_GATEWAY_TRANSPORT_ERROR_NO_KEY:
			return "Model call refused: the gateway holds no key for " + c.GetProvider()
		case pb.GatewayTransportError_GATEWAY_TRANSPORT_ERROR_DIAL:
			return "Model call failed: could not reach " + c.GetProvider()
		case pb.GatewayTransportError_GATEWAY_TRANSPORT_ERROR_TLS:
			return "Model call failed: TLS error talking to " + c.GetProvider()
		case pb.GatewayTransportError_GATEWAY_TRANSPORT_ERROR_TIMEOUT:
			return "Model call failed: " + c.GetProvider() + " timed out"
		case pb.GatewayTransportError_GATEWAY_TRANSPORT_ERROR_CANCELED:
			return "Model call cancelled before " + c.GetProvider() + " answered"
		}
		return "Model call failed before " + c.GetProvider() + " answered"
	}
	st := c.GetStatus()
	switch {
	case st == 401 || st == 403:
		return fmt.Sprintf("Model call refused: credential invalid (%d)", st)
	case st == 429:
		return fmt.Sprintf("Model call refused: rate limited (%d)", st)
	case st >= 500:
		return fmt.Sprintf("Model call failed: %s returned an error (%d)", c.GetProvider(), st)
	case st >= 400:
		return fmt.Sprintf("Model call rejected (%d)", st)
	}
	return fmt.Sprintf("Model call ok (%d)", st)
}

// journalTime reads the "t" field of one journal line; ok is false for a line
// that has none or that is not JSON.
func journalTime(line []byte) (time.Time, bool) {
	var l struct {
		T string `json:"t"`
	}
	if json.Unmarshal(line, &l) != nil || l.T == "" {
