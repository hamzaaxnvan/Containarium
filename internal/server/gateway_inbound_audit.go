package server

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/footprintai/containarium/internal/audit"
	"github.com/footprintai/containarium/internal/modelgateway"
)

// modelGatewayInboundBlockAction is the audit action of one response the
// model gateway's inbound scan blocked (#2367).
const modelGatewayInboundBlockAction = "model_gateway.inbound_block"

// inboundBlockAuditDetail is the Detail of a model_gateway.inbound_block
// entry: attribution, the typed reason, finding kinds and counts. Never the
// flagged text or the response body.
type inboundBlockAuditDetail struct {
	Skill          string   `json:"skill,omitempty"`
	Provider       string   `json:"provider"`
	Model          string   `json:"model,omitempty"`
	Reason         string   `json:"reason"`
	Kinds          []string `json:"kinds,omitempty"`
	Findings       int      `json:"findings"`
	EngineID       string   `json:"engine_id,omitempty"`
	PolicyRevision int64    `json:"policy_revision"`
}

// gatewayInboundAudit writes inbound blocks and baseline detections to the
// daemon's audit log. It satisfies modelgateway.InboundAuditSink; the gateway
// calls it from its audit worker, never on the request path.
type gatewayInboundAudit struct {
	log auditLogger
}

var _ modelgateway.InboundAuditSink = gatewayInboundAudit{}

func (a gatewayInboundAudit) RecordInboundBlock(ctx context.Context, b modelgateway.InboundBlock) error {
	d := inboundBlockAuditDetail{
		Skill: b.SkillID, Provider: b.Provider, Model: b.Model, Reason: b.Reason.String(),
		Findings: b.Findings, EngineID: b.EngineID, PolicyRevision: b.PolicyRevision,
