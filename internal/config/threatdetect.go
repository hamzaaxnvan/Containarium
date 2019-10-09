package config

import "time"

// EnvThreatSentry is the opt-in flag for the continuous threat-detection
// background sentry (#1640) — off by default, consistent with the
// platform's other opt-in security features (network-policy enforce,
// signature scanning above).
const EnvThreatSentry = "CONTAINARIUM_THREAT_SENTRY"

// EnvThreatDenyBurstN and EnvThreatDenyBurstWindowMinutes tune the
// deny-burst fence-probe rule (#1642): N deny events for one tenant within
// M minutes raises a MEDIUM finding. Operator-tunable without a daemon
// rebuild — set the env var and restart. Live tuning via an
// UpdateThreatRuleConfig RPC without a restart is #1643's scope.
const (
	EnvThreatDenyBurstN             = "CONTAINARIUM_THREAT_DENY_BURST_N"
	EnvThreatDenyBurstWindowMinutes = "CONTAINARIUM_THREAT_DENY_BURST_WINDOW_MINUTES"
)

// EnvThreatDenyBurstCountLogOnly makes the deny-burst rule also count
// would-deny events from tenants whose policy is observation-only (#2612).
// Off by default: the rule counts enforced drops only.
const EnvThreatDenyBurstCountLogOnly = "CONTAINARIUM_THREAT_DENY_BURST_COUNT_LOG_ONLY"

// EnvThreatWebhookRenotifyMinutes sets how long an open finding stays quiet
// on the webhook after a delivery before the same condition is delivered
// again (#2643). Unset or non-positive keeps the 1h default.
const EnvThreatWebhookRenotifyMinutes = "CONTAINARIUM_THREAT_WEBHOOK_RENOTIFY_MINUTES"

// ThreatDetect is the typed view of the CONTAINARIUM_THREAT_* namespace.
