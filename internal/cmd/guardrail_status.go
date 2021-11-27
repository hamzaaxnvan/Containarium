package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/footprintai/containarium/internal/guardrailpolicy"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// `containarium guardrail status` — what scans model responses on the daemon
// (#2737): none, baseline or admin. Talks to the daemon, like `policy get`.

var guardrailStatusJSON bool

// guardrailStatusAPI is the slice of the typed clients `status` uses.
type guardrailStatusAPI interface {
	GetGuardrailStatus() (*pb.GetGuardrailStatusResponse, error)
}

// newGuardrailStatusAPI builds the client from the global flags. A variable
// so tests can substitute a fake.
var newGuardrailStatusAPI = func() (guardrailStatusAPI, func(), error) {
