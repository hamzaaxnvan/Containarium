package server

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/safecast"
	zapscanner "github.com/footprintai/containarium/internal/zap"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ZapServer implements the ZapService gRPC service
type ZapServer struct {
	pb.UnimplementedZapServiceServer
	store     *zapscanner.Store
	manager   *zapscanner.Manager
	installer *zapscanner.Installer
}

// NewZapServer creates a new ZAP server
func NewZapServer(store *zapscanner.Store, manager *zapscanner.Manager) *ZapServer {
	return &ZapServer{
		store:     store,
		manager:   manager,
		installer: zapscanner.NewInstaller(),
	}
}

// TriggerZapScan triggers an on-demand scan (non-blocking).
// Admin-only: ZAP scans are cluster-wide security operations that
// can target arbitrary containers; restricted to operators.
func (s *ZapServer) TriggerZapScan(ctx context.Context, req *pb.TriggerZapScanRequest) (*pb.TriggerZapScanResponse, error) {
	if err := auth.RequireScope(ctx, auth.ScopeSecurityWrite); err != nil {
		return nil, err
	}
	if err := auth.RequireRole(ctx, auth.RoleAdmin); err != nil {
		return nil, err
	}
	if s.manager == nil {
		return nil, fmt.Errorf("ZAP scanner is not available")
	}

	scanRunID, err := s.manager.RunScan(ctx, "manual", req.ContainerName)
	if errors.Is(err, zapscanner.ErrZapNotInstalled) {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	if err != nil {
		return nil, fmt.Errorf("failed to trigger ZAP scan: %w", err)
	}

	msg := "ZAP scan enqueued — workers processing asynchronously"
	if req.ContainerName != "" {
		msg = fmt.Sprintf("ZAP scan enqueued (scope: %s) — workers processing asynchronously", req.ContainerName)
	}
	return &pb.TriggerZapScanResponse{
		ScanRunId: scanRunID,
		Message:   msg,
	}, nil
}

// ListZapScanRuns returns recent scan runs. Admin-only —
// security scan history can name arbitrary containers.
func (s *ZapServer) ListZapScanRuns(ctx context.Context, req *pb.ListZapScanRunsRequest) (*pb.ListZapScanRunsResponse, error) {
	if err := auth.RequireScope(ctx, auth.ScopeSecurityRead); err != nil {
		return nil, err
	}
	if err := auth.RequireRole(ctx, auth.RoleAdmin); err != nil {
		return nil, err
	}
	runs, totalCount, err := s.store.ListScanRuns(ctx, int(req.Limit), int(req.Offset), req.ContainerName)
	if err != nil {
		return nil, fmt.Errorf("failed to list ZAP scan runs: %w", err)
	}

	var pbRuns []*pb.ZapScanRun
	for _, run := range runs {
		pbRun := zapScanRunToProto(&run)
		if run.Status == "running" {
			if count, err := s.store.CountFinishedJobs(ctx, run.ID); err == nil {
				pbRun.CompletedCount = safecast.I32(count)
			}
		} else {
			pbRun.CompletedCount = safecast.I32(run.TargetsCount)
		}
		pbRuns = append(pbRuns, pbRun)
	}

	return &pb.ListZapScanRunsResponse{
		ScanRuns:   pbRuns,
		TotalCount: totalCount,
	}, nil
}

// ListZapAlerts returns alerts with optional filtering.
// Admin-only — security alerts cross tenants.
func (s *ZapServer) ListZapAlerts(ctx context.Context, req *pb.ListZapAlertsRequest) (*pb.ListZapAlertsResponse, error) {
	if err := auth.RequireScope(ctx, auth.ScopeSecurityRead); err != nil {
		return nil, err
	}
	if err := auth.RequireRole(ctx, auth.RoleAdmin); err != nil {
		return nil, err
	}
	params := zapscanner.AlertListParams{
		Risk:   req.Risk,
		Status: req.Status,
		Domain: req.Domain,
