package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/security"
	"github.com/footprintai/containarium/pkg/core/incus"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
)

// SecurityServer implements the SecurityService gRPC service
type SecurityServer struct {
	pb.UnimplementedSecurityServiceServer
	store          *security.Store
	incusClient    *incus.Client
	scanner        *security.Scanner
	peerPool       *PeerPool
	localBackendID string
}

// SetPeerPool sets the peer pool for including peer containers in security summaries.
func (s *SecurityServer) SetPeerPool(pool *PeerPool) {
	s.peerPool = pool
	if pool != nil {
		s.localBackendID = pool.LocalBackendID()
	}
}

// NewSecurityServer creates a new security server
func NewSecurityServer(store *security.Store, incusClient *incus.Client, scanner *security.Scanner) *SecurityServer {
	return &SecurityServer{
		store:       store,
		incusClient: incusClient,
		scanner:     scanner,
	}
}

// ListClamavReports returns ClamAV scan reports with optional filtering.
// Phase 1.4 — when ContainerName is set, tenant authz via the
// owner derivation; when blank, the listing would cover all
// containers, so require admin.
func (s *SecurityServer) ListClamavReports(ctx context.Context, req *pb.ListClamavReportsRequest) (*pb.ListClamavReportsResponse, error) {
	if err := auth.RequireScope(ctx, auth.ScopeSecurityRead); err != nil {
		return nil, err
	}
	if req.ContainerName == "" {
		if err := auth.RequireRole(ctx, auth.RoleAdmin); err != nil {
			return nil, err
		}
	} else {
		if err := auth.AuthorizeContainerAccess(ctx, req.ContainerName); err != nil {
			return nil, err
		}
	}
	params := security.ListParams{
		ContainerName: req.ContainerName,
		Status:        req.Status,
		From:          req.From,
		To:            req.To,
		Limit:         int(req.Limit),
		Offset:        int(req.Offset),
	}

	reports, totalCount, err := s.store.ListReports(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("failed to list reports: %w", err)
	}

	// Tag local reports with backend_id
	for _, r := range reports {
		if r.BackendId == "" {
			r.BackendId = s.localBackendID
		}
	}

	// Merge reports from peers
	if s.peerPool != nil {
		authToken := extractAuthToken(ctx)
		peerReports := s.fetchPeerReports(authToken, req)
		reports = append(reports, peerReports...)
		totalCount += int32(len(peerReports)) // #nosec G115 -- value bounded by container/scan count
	}

	return &pb.ListClamavReportsResponse{
		Reports:    reports,
		TotalCount: totalCount,
	}, nil
}

// fetchPeerReports fetches ClamAV reports from all peers in parallel.
func (s *SecurityServer) fetchPeerReports(authToken string, req *pb.ListClamavReportsRequest) []*pb.ClamavReport {
