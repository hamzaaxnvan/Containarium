//go:build !containarium_client

package server

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/cluster"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// KubeconfigReader reads a READY cluster's admin kubeconfig on demand
// from its control-plane VM. Implemented by the provisioner (#1414);
// the platform never persists kubeconfigs (design: managed-k8s-clusters,
// "Credentials").
type KubeconfigReader interface {
	ReadKubeconfig(ctx context.Context, c *cluster.Cluster) (string, error)
}

// ClusterServer implements ClusterService (#1413): managed-cluster
// lifecycle state. Provisioning is asynchronous by design — handlers
// record desired state and the reconciler (#1414) converges VMs — so
// this server stays thin: authz, validation, store transitions.
//
// Without a reconciler wired (SetReconciler), DeleteCluster removes
// rows directly (nothing can have provisioned VMs) and
// GetClusterKubeconfig answers Unimplemented on a READY cluster.
type ClusterServer struct {
	pb.UnimplementedClusterServiceServer
	store      cluster.Store
	kubeconfig KubeconfigReader
	// nodeCapable is the create-time capability probe (#1414, #1429):
	// dispatched on the requested isolation class, it refuses cluster
	// creation with a typed error instead of provisioning doomed
	// records — a VM cluster on a KVM-less host, a container cluster
	// on a host missing the container-node preconditions.
	nodeCapable func(cluster.Isolation) error
	// asyncDelete marks the reconciler as wired: DeleteCluster flips
	// records to DELETING for the reconciler to drain instead of
	// dropping rows that may have live VMs behind them.
	asyncDelete bool
	// caps is the operator ceiling on configurable cluster size
	// (#1417); zero values = unlimited.
	caps clusterCaps
	// isolation is the operator's per-host opt-in for the weaker node
	// isolation class (#1428). Zero value = container nodes refused.
	isolation isolationGate
}

// NewClusterServer builds the server on a Store (in-memory at startup;
// swapped to Postgres once the pool is up, same pattern as the network
// policy and crew-run stores).
func NewClusterServer(store cluster.Store) *ClusterServer {
	return &ClusterServer{store: store}
}

// SetStore swaps the backing store. Called before grpcServer.Serve, so
// it races with no live RPCs.
func (s *ClusterServer) SetStore(store cluster.Store) { s.store = store }

// SetKubeconfigReader wires the provisioner-backed kubeconfig path (#1414).
func (s *ClusterServer) SetKubeconfigReader(r KubeconfigReader) { s.kubeconfig = r }

// Store exposes the backing store so the reconciler shares it.
func (s *ClusterServer) Store() cluster.Store { return s.store }

// SetReconciler wires the #1414 reconciler: kubeconfig reads, the VM
// capability probe, and reconciler-drained deletes.
func (s *ClusterServer) SetReconciler(r *ClusterReconciler) {
	s.kubeconfig = r
	s.nodeCapable = r.NodeCapable
	s.asyncDelete = true
}

// resolveOwner defaults an empty request owner to the authenticated
// subject and enforces tenant isolation for the resolved value.
func resolveOwner(ctx context.Context, reqOwner string) (string, error) {
	if reqOwner == "" {
		username, _, ok := auth.SubjectFromGRPCContext(ctx)
		if !ok {
			return "", status.Error(codes.Unauthenticated, "no authenticated subject")
		}
		reqOwner = username
	}
	if err := auth.AuthorizeTenant(ctx, reqOwner); err != nil {
		return "", err
	}
	return reqOwner, nil
}

func storeErr(err error) error {
	switch {
	case errors.Is(err, cluster.ErrNotFound):
		return status.Error(codes.NotFound, "cluster not found")
	case errors.Is(err, cluster.ErrAlreadyExists):
		return status.Error(codes.AlreadyExists, "cluster already exists")
	default:
		return status.Errorf(codes.Internal, "%v", err)
	}
}

// groupsFromProto converts and screens the typed node groups. GPU and
// per-box storage-class fields exist on the shared ResourceLimits
// message but have no meaning on a cluster node in v1 — GPU pools are
// a later phase and storage_class is a box-PVC concept — so requests
// carrying either are rejected loudly rather than silently dropped.
func groupsFromProto(in []*pb.NodeGroup) ([]cluster.NodeGroup, error) {
	out := make([]cluster.NodeGroup, 0, len(in))
	for _, g := range in {
		if g.Size == nil {
			return nil, status.Errorf(codes.InvalidArgument, "node group %q: size is required", g.Name)
		}
		if g.Size.Gpu != "" || len(g.Size.Gpus) > 0 { //nolint:staticcheck // deliberately reading the deprecated field to reject it
			return nil, status.Errorf(codes.InvalidArgument, "node group %q: GPU node pools are not supported yet (later phase)", g.Name)
		}
		if g.Size.StorageClass != "" {
			return nil, status.Errorf(codes.InvalidArgument, "node group %q: storage_class does not apply to cluster nodes", g.Name)
		}
		out = append(out, cluster.NodeGroup{
			Name:     g.Name,
			Size:     cluster.Size{CPU: g.Size.Cpu, Memory: g.Size.Memory, Disk: g.Size.Disk},
			MinNodes: g.MinNodes,
			MaxNodes: g.MaxNodes,
		})
	}
	return out, nil
}

func groupToProto(g cluster.NodeGroup) *pb.NodeGroup {
	return &pb.NodeGroup{
		Name:     g.Name,
		Size:     &pb.ResourceLimits{Cpu: g.Size.CPU, Memory: g.Size.Memory, Disk: g.Size.Disk},
		MinNodes: g.MinNodes,
		MaxNodes: g.MaxNodes,
	}
}

var stateToProto = map[cluster.State]pb.ClusterState{
	cluster.StateProvisioning: pb.ClusterState_CLUSTER_STATE_PROVISIONING,
	cluster.StateReady:        pb.ClusterState_CLUSTER_STATE_READY,
	cluster.StateDegraded:     pb.ClusterState_CLUSTER_STATE_DEGRADED,
	cluster.StateDeleting:     pb.ClusterState_CLUSTER_STATE_DELETING,
	cluster.StateError:        pb.ClusterState_CLUSTER_STATE_ERROR,
}
