package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/footprintai/containarium/internal/alert"
	"github.com/footprintai/containarium/internal/app"
	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/internal/safecast"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// SetAlertManager sets the alert store and manager on the container server.
// Called from dual_server.go after the alert components are initialized.
func (s *ContainerServer) SetAlertManager(store *alert.Store, manager *alert.Manager, webhookURL string, webhookSecret string, coreServices *CoreServices, daemonConfigStore *app.DaemonConfigStore) {
	s.alertStore = store
	s.alertManager = manager
	s.alertWebhookURL = webhookURL
	s.alertWebhookSecret = webhookSecret
	s.coreServices = coreServices
	// Nil-guarded: the field is an interface (daemonConfigKV), and
	// wrapping a nil *app.DaemonConfigStore would defeat every
	// `!= nil` check downstream. SetDaemonConfigStore wires it much
	// earlier in startup; this assignment is kept for callers that
	// only go through the alert path.
	if daemonConfigStore != nil {
		s.daemonConfigStore = daemonConfigStore
	}
}

// SetAlertDeliveryStore sets the delivery store for recording webhook delivery attempts
func (s *ContainerServer) SetAlertDeliveryStore(ds *alert.DeliveryStore) {
	s.alertDeliveryStore = ds
}

// CreateAlertRule creates a new custom alert rule. Admin-only
// — alert rules drive cluster-wide notifications.
func (s *ContainerServer) CreateAlertRule(ctx context.Context, req *pb.CreateAlertRuleRequest) (*pb.CreateAlertRuleResponse, error) {
	if err := auth.RequireScope(ctx, auth.ScopeAlertsWrite); err != nil {
		return nil, err
	}
	if err := auth.RequireRole(ctx, auth.RoleAdmin); err != nil {
		return nil, err
	}
	if s.alertStore == nil {
		return nil, status.Error(codes.Unavailable, "alerting is not configured")
	}

	if req.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "name is required")
	}
	if req.Expr == "" {
		return nil, status.Error(codes.InvalidArgument, "expr is required")
	}

	severity := req.Severity
	if severity == "" {
		severity = alert.SeverityWarning
	}
	if err := alert.ValidateSeverity(severity); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	duration := req.Duration
	if duration == "" {
		duration = "5m"
	}

	rule := &alert.AlertRule{
		Name:        req.Name,
		Expr:        req.Expr,
		Duration:    duration,
		Severity:    severity,
		Description: req.Description,
		Labels:      req.Labels,
		Annotations: req.Annotations,
		Enabled:     req.Enabled,
	}

	created, err := s.alertStore.Create(ctx, rule)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create alert rule: %v", err)
	}

	// Sync rules to vmalert (best-effort)
	if s.alertManager != nil {
		if err := s.alertManager.SyncRules(ctx); err != nil {
			log.Printf("Warning: failed to sync alert rules after create: %v", err)
		}
	}

	return &pb.CreateAlertRuleResponse{
		Rule: alertRuleToProto(created),
	}, nil
}

// ListAlertRules lists all custom alert rules. Admin-only —
// rule names + expressions can disclose operator practice.
func (s *ContainerServer) ListAlertRules(ctx context.Context, req *pb.ListAlertRulesRequest) (*pb.ListAlertRulesResponse, error) {
	if err := auth.RequireScope(ctx, auth.ScopeAlertsRead); err != nil {
		return nil, err
	}
	if err := auth.RequireRole(ctx, auth.RoleAdmin); err != nil {
		return nil, err
	}
	if s.alertStore == nil {
		return &pb.ListAlertRulesResponse{}, nil
	}

	rules, err := s.alertStore.List(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list alert rules: %v", err)
	}

	pbRules := make([]*pb.AlertRule, len(rules))
	for i, rule := range rules {
		pbRules[i] = alertRuleToProto(rule)
	}

	return &pb.ListAlertRulesResponse{Rules: pbRules}, nil
}

// GetAlertRule gets a single alert rule by ID. Admin-only.
func (s *ContainerServer) GetAlertRule(ctx context.Context, req *pb.GetAlertRuleRequest) (*pb.GetAlertRuleResponse, error) {
	if err := auth.RequireScope(ctx, auth.ScopeAlertsRead); err != nil {
		return nil, err
	}
	if err := auth.RequireRole(ctx, auth.RoleAdmin); err != nil {
		return nil, err
	}
	if s.alertStore == nil {
		return nil, status.Error(codes.Unavailable, "alerting is not configured")
	}

	if req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}

	rule, err := s.alertStore.Get(ctx, req.Id)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil, status.Errorf(codes.NotFound, "alert rule not found: %s", req.Id)
		}
		return nil, status.Errorf(codes.Internal, "failed to get alert rule: %v", err)
	}

	return &pb.GetAlertRuleResponse{Rule: alertRuleToProto(rule)}, nil
}

// UpdateAlertRule updates an existing alert rule. Admin-only.
func (s *ContainerServer) UpdateAlertRule(ctx context.Context, req *pb.UpdateAlertRuleRequest) (*pb.UpdateAlertRuleResponse, error) {
	if err := auth.RequireScope(ctx, auth.ScopeAlertsWrite); err != nil {
		return nil, err
	}
	if err := auth.RequireRole(ctx, auth.RoleAdmin); err != nil {
		return nil, err
	}
	if s.alertStore == nil {
		return nil, status.Error(codes.Unavailable, "alerting is not configured")
	}

	if req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}

	// Get existing rule to preserve fields not in the update
	existing, err := s.alertStore.Get(ctx, req.Id)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil, status.Errorf(codes.NotFound, "alert rule not found: %s", req.Id)
		}
		return nil, status.Errorf(codes.Internal, "failed to get alert rule: %v", err)
	}

	// Apply updates
	if req.Name != "" {
		existing.Name = req.Name
	}
	if req.Expr != "" {
		existing.Expr = req.Expr
	}
	if req.Duration != "" {
		existing.Duration = req.Duration
	}
	if req.Severity != "" {
		if err := alert.ValidateSeverity(req.Severity); err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		existing.Severity = req.Severity
	}
	if req.Description != "" {
		existing.Description = req.Description
	}
	if req.Labels != nil {
		existing.Labels = req.Labels
	}
	if req.Annotations != nil {
		existing.Annotations = req.Annotations
	}
	if req.Enabled != nil {
		existing.Enabled = *req.Enabled
	}

	updated, err := s.alertStore.Update(ctx, existing)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update alert rule: %v", err)
	}

	// Sync rules to vmalert (best-effort)
	if s.alertManager != nil {
		if err := s.alertManager.SyncRules(ctx); err != nil {
			log.Printf("Warning: failed to sync alert rules after update: %v", err)
		}
	}

	return &pb.UpdateAlertRuleResponse{Rule: alertRuleToProto(updated)}, nil
}

// DeleteAlertRule deletes an alert rule. Admin-only — rule
// removal silently stops paging on real issues.
func (s *ContainerServer) DeleteAlertRule(ctx context.Context, req *pb.DeleteAlertRuleRequest) (*pb.DeleteAlertRuleResponse, error) {
	if err := auth.RequireScope(ctx, auth.ScopeAlertsWrite); err != nil {
		return nil, err
