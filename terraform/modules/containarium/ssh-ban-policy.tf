# Rendered independently of VM resources so policy can be tested without GCP.
locals {
  ssh_upstream_policy_json = jsonencode({
    initialDelay  = "1s", maxDelay = "30s", window = "${var.ssh_upstream_window_seconds}s", setupTimeout = "30s",
    failureBudget = var.ssh_upstream_failure_budget, riskPerAttempt = var.ssh_upstream_risk_per_attempt,
    maxBackends   = 1024, maxPending = 32, maxConnections = 4096, jitter = 0.25, egressGroup = "sentinel"
  })
