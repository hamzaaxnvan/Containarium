package modelgateway

import (
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/footprintai/containarium/internal/guardrail"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Baseline response scan (#2736, slice A of #2575). When the policy store
// answers "not configured" the response is scanned against
// guardrail.BaselineRules, log-only. Unlike the admin-policy hold in
// inboundRequest.enforce, nothing is held: the body is teed to the client as
// it arrives, and the copy is scanned once the proxy has finished writing it
// (on Close), so a streamed response on a default install is not delayed.
// A detection is logged, counted and audited; it never blocks.

// baselineRequest is one response under the baseline.
type baselineRequest struct {
	in  *inbound
	sub inboundSubject
}

// watch wraps the final response body. It runs as the last step of
// ModifyResponse, so it sees exactly the bytes the client is sent.
func (br *baselineRequest) watch(resp *http.Response) {
	if resp.Body == nil || resp.Body == http.NoBody {
		return
	}
	// Encoded bytes are opaque to the rules; count, never block.
	if resp.Header.Get("Content-Encoding") != "" {
		br.in.baselineUnscanned.Add(1)
		return
	}
	resp.Body = &baselineTee{
		src: resp.Body, br: br, limit: br.in.limit,
		streaming: strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream"),
	}
}

// baselineTee passes reads through unchanged and keeps a bounded copy whose
// capacity never exceeds the limit. Past the limit the copy is dropped (the
// response still flows) and the response is counted as unscanned.
type baselineTee struct {
	src       io.ReadCloser
	br        *baselineRequest
	limit     int
	streaming bool
	buf       []byte
	over      bool
