// Package k8s implements box.BoxBackend on Kubernetes, declaring one
// kubernetes-sigs/agent-sandbox Sandbox CR per box: a per-tenant pod an
// agent reaches over SSH, serving the same agent-box stdio MCP surface as the
// LXC backend. See docs/K8S-AGENT-BOX-RUNTIME-DESIGN.md.
