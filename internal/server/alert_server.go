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

	if err := s.alertStore.Delete(ctx, req.Id); err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil, status.Errorf(codes.NotFound, "alert rule not found: %s", req.Id)
		}
		return nil, status.Errorf(codes.Internal, "failed to delete alert rule: %v", err)
	}

	// Sync rules to vmalert (best-effort)
	if s.alertManager != nil {
		if err := s.alertManager.SyncRules(ctx); err != nil {
			log.Printf("Warning: failed to sync alert rules after delete: %v", err)
		}
	}

	return &pb.DeleteAlertRuleResponse{}, nil
}

// GetAlertingInfo returns alerting system status
func (s *ContainerServer) GetAlertingInfo(ctx context.Context, req *pb.GetAlertingInfoRequest) (*pb.GetAlertingInfoResponse, error) {
	// Reports alerting health, the masked webhook URL and whether a webhook
	// secret is configured. alerts:read matches this file's own convention (#1718).
	if err := auth.RequireScope(ctx, auth.ScopeAlertsRead); err != nil {
		return nil, err
	}
	resp := &pb.GetAlertingInfoResponse{
		Enabled: s.alertStore != nil,
	}

	if s.alertStore == nil {
		return resp, nil
	}

	// Check vmalert health
	resp.VmalertStatus = checkHTTPHealth(fmt.Sprintf("http://%s:%d/-/healthy", s.victoriaMetricsIP(), DefaultVMAlertPort))

	// Check alertmanager health (path includes external-url prefix)
	resp.AlertmanagerStatus = checkHTTPHealth(fmt.Sprintf("http://%s:%d/alertmanager/-/healthy", s.victoriaMetricsIP(), DefaultAlertmanagerPort))

	// Mask webhook URL for security
	if s.alertWebhookURL != "" {
		resp.WebhookUrl = maskURL(s.alertWebhookURL)
	}

	// Indicate whether a signing secret is configured
	resp.WebhookSecretConfigured = s.alertWebhookSecret != ""

	// Count rules
	rules, err := s.alertStore.List(ctx)
	if err == nil {
		resp.CustomRules = safecast.I32(len(rules))
	}
	// Default rules count is static (9 rules in default.yml)
	resp.TotalRules = 9 + resp.CustomRules

	return resp, nil
}

// victoriaMetricsIP returns the IP of the VictoriaMetrics container
func (s *ContainerServer) victoriaMetricsIP() string {
	// Extract IP from URL like "http://10.100.0.5:8428"
	url := s.victoriaMetricsURL
	if url == "" {
		return "localhost"
	}
	url = strings.TrimPrefix(url, "http://")
	url = strings.TrimPrefix(url, "https://")
	if idx := strings.Index(url, ":"); idx > 0 {
		return url[:idx]
	}
	return url
}

// checkHTTPHealth performs a simple HTTP GET and returns "healthy" or "unhealthy"
func checkHTTPHealth(url string) string {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "unhealthy"
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return "healthy"
	}
	return "unhealthy"
}

// maskURL masks the URL for display (shows host but hides path/query)
func maskURL(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	// Show scheme + host, mask the rest
	parts := strings.SplitN(rawURL, "//", 2)
	if len(parts) < 2 {
		return "***"
	}
	hostPart := parts[1]
	if idx := strings.Index(hostPart, "/"); idx > 0 {
		return parts[0] + "//" + hostPart[:idx] + "/***"
	}
	return rawURL
}

// ListDefaultAlertRules returns the built-in default alert rules
func (s *ContainerServer) ListDefaultAlertRules(ctx context.Context, req *pb.ListDefaultAlertRulesRequest) (*pb.ListDefaultAlertRulesResponse, error) {
	// Static rule catalog, gated on the same read scope as the rest of this
	// file rather than left open (#1718).
	if err := auth.RequireScope(ctx, auth.ScopeAlertsRead); err != nil {
		return nil, err
	}
	rules, err := parseDefaultAlertRules()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to parse default rules: %v", err)
	}
	return &pb.ListDefaultAlertRulesResponse{Rules: rules}, nil
}

// parseDefaultAlertRules parses the DefaultAlertRules YAML constant into protobuf AlertRule messages
func parseDefaultAlertRules() ([]*pb.AlertRule, error) {
	var rulesFile struct {
		Groups []struct {
			Name  string `yaml:"name"`
			Rules []struct {
				Alert       string            `yaml:"alert"`
				Expr        string            `yaml:"expr"`
				For         string            `yaml:"for"`
				Labels      map[string]string `yaml:"labels"`
				Annotations map[string]string `yaml:"annotations"`
			} `yaml:"rules"`
		} `yaml:"groups"`
	}

	if err := yaml.Unmarshal([]byte(DefaultAlertRules), &rulesFile); err != nil {
		return nil, fmt.Errorf("failed to parse default rules YAML: %w", err)
	}

	var pbRules []*pb.AlertRule
	for _, group := range rulesFile.Groups {
		for _, rule := range group.Rules {
			severity := rule.Labels["severity"]
			description := rule.Annotations["description"]
			pbRules = append(pbRules, &pb.AlertRule{
				Id:          fmt.Sprintf("default-%s", strings.ToLower(rule.Alert)),
				Name:        rule.Alert,
				Expr:        rule.Expr,
				Duration:    rule.For,
				Severity:    severity,
				Description: description,
				Labels:      rule.Labels,
				Annotations: rule.Annotations,
				Enabled:     true,
			})
		}
	}
	return pbRules, nil
}

// UpdateAlertingConfig updates the alerting system configuration
// (webhook URL and/or secret). Admin-only — webhook URL controls
// where alert payloads (potentially containing tenant data) are
// posted; redirecting it is exfiltration.
func (s *ContainerServer) UpdateAlertingConfig(ctx context.Context, req *pb.UpdateAlertingConfigRequest) (*pb.UpdateAlertingConfigResponse, error) {
	if err := auth.RequireScope(ctx, auth.ScopeAlertsWrite); err != nil {
		return nil, err
	}
	if err := auth.RequireRole(ctx, auth.RoleAdmin); err != nil {
		return nil, err
	}
	if s.alertStore == nil {
		return nil, status.Error(codes.Unavailable, "alerting is not configured")
	}

	if s.coreServices == nil {
		return nil, status.Error(codes.Unavailable, "core services not available for webhook update")
	}

	// Handle webhook secret generation
	var generatedSecret string
	if req.GenerateWebhookSecret {
		generatedSecret = generateWebhookSecret()
		s.alertWebhookSecret = generatedSecret

		// Persist secret to database
		if s.daemonConfigStore != nil {
			if err := s.daemonConfigStore.Set(ctx, "alert_webhook_secret", generatedSecret); err != nil {
				log.Printf("Warning: failed to persist webhook secret to database: %v", err)
			} else {
				log.Printf("Webhook secret generated and persisted to database")
			}
		}

		// Update gateway relay config if available
		if s.alertRelayConfigFn != nil {
			s.alertRelayConfigFn(s.alertWebhookURL, s.alertWebhookSecret)
		}
	}

	// Update webhook URL if provided (or explicitly clearing it)
	webhookURL := req.WebhookUrl
	if webhookURL != "" || !req.GenerateWebhookSecret {
		// Route through relay if a signing secret is configured
		relayURL := webhookURL
		if s.alertWebhookSecret != "" && webhookURL != "" && s.hostRelayURL != "" {
			relayURL = s.hostRelayURL
		}

		// Update alertmanager config in the container
		if err := s.coreServices.UpdateAlertmanagerWebhook(ctx, relayURL); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to update webhook: %v", err)
		}

		// Update in-memory webhook URL
		s.alertWebhookURL = webhookURL

		// Update gateway relay config if available
		if s.alertRelayConfigFn != nil {
			s.alertRelayConfigFn(s.alertWebhookURL, s.alertWebhookSecret)
		}

		// Persist to database if config store is available
		if s.daemonConfigStore != nil {
			if err := s.daemonConfigStore.Set(ctx, "alert_webhook_url", webhookURL); err != nil {
				log.Printf("Warning: failed to persist webhook URL to database: %v", err)
			} else {
				log.Printf("Webhook URL persisted to database")
			}
		}
