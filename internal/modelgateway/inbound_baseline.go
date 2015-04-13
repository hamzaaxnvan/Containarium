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

