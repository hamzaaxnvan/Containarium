package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/footprintai/containarium/internal/client"
	"github.com/footprintai/containarium/internal/guardrailpolicy"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// `containarium guardrail policy get|set` — the server-side guardrail policy
// (#2368; docs/architecture/guardrail-inbound-and-server-policy.md). Unlike
// the other guardrail verbs, these talk to the daemon: the policy lives on
// the platform. They carry no dataset content.

var (
	guardrailPolicyJSON    bool
	guardrailPolicySetFile string
)

// guardrailPolicyAPI is the slice of the typed clients these verbs use; both
// client.GRPCClient and client.HTTPClient satisfy it.
type guardrailPolicyAPI interface {
	GetGuardrailPolicy() (*pb.GetGuardrailPolicyResponse, error)
	SetGuardrailPolicy(req *pb.SetGuardrailPolicyRequest) (*pb.SetGuardrailPolicyResponse, error)
}

// errNoGuardrailServer is newGuardrailPolicyAPI's answer when no --server is
// configured. `policy get|set` report it; `apply` and `verify` read it as "no
// daemon", their local mode (guardrail_server.go).
var errNoGuardrailServer = errors.New("--server is required for guardrail policy commands")

// newGuardrailPolicyAPI builds the client from the global --server/--http
// flags. A variable so tests can substitute a fake.
