//go:build !containarium_client

package server

// Node isolation gate (#1428). A cluster's isolation class decides what
// contains a tenant kernel exploit: a VM node keeps it inside the
// tenant's own kernel, a container node does not. The weaker class is
// therefore not tenant-selectable — the tenant *requests* it and the
// operator *permits* it, per host, with an env opt-in.
//
// Design: docs/architecture/cluster-container-node-pools.md.

import (
	"fmt"
	"strconv"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/cluster"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// allowContainerNodesEnv is the operator's per-host assertion that
// everything on this host already shares one trust domain (a dev rung,
// a dedicated single-tenant machine, the operator's own CI). Unset on
// a shared multi-tenant host, container clusters are impossible there,
// so a tenant can never downgrade the boundary protecting another
// tenant.
const allowContainerNodesEnv = "CONTAINARIUM_CLUSTER_ALLOW_CONTAINER_NODES"

// isolationGate holds the parsed opt-in. Same fail-closed shape as
// clusterCaps: a configuration that cannot be parsed poisons the gate
// with configErr rather than leaving it open.
type isolationGate struct {
	allowContainer bool
	configErr      error
}

// parseIsolationGate reads the opt-in env. Unset is "not permitted"
// with no error (the default posture, not a misconfiguration); a value
