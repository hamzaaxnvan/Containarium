package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/footprintai/containarium/internal/client"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"github.com/spf13/cobra"
	"io"
)

var accessBoth, accessRevoke, accessConfirm bool

// accessEdit is one reviewed destination change in a plan file. The plan stays
// a plain reviewable JSON document; apply converts it to the typed proto.
type accessEdit struct {
	Destination string   `json:"destination"`
	Add         []string `json:"addSources,omitempty"`
	Remove      []string `json:"removeSources,omitempty"`
	Revision    string   `json:"expectedRevision"`
}
type accessPlan struct {
	Changes []accessEdit `json:"changes"`
}

// tenantAccessClient is the typed surface the access commands use. Both
// transports satisfy it.
type tenantAccessClient interface {
	GetNetworkPolicy(tenant string) (*pb.GetNetworkPolicyResponse, error)
	PatchTenantAccess(req *pb.PatchTenantAccessRequest) (*pb.PatchTenantAccessResponse, error)
	Close() error
}

func newTenantAccessClient() (tenantAccessClient, error) {
	if serverAddr == "" {
		return nil, errServerRequired()
	}
	// The network-policy command family uses the daemon HTTP API.
	return client.NewHTTPClient(serverAddr, authToken)
}

func init() {
	parent := &cobra.Command{Use: "access", Short: "Preview/apply atomic cross-Tenant ingress changes (admin)"}
	preview := &cobra.Command{Use: "preview <source> <destination>", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newTenantAccessClient()
