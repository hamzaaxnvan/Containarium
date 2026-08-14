# containarium-agent-box
#
# The in-the-box image for the Kubernetes backend (docs/K8S-AGENT-BOX-RUNTIME-DESIGN.md):
# a minimal SSH server whose every session is pinned by forced command to
# `agent-box` (the in-the-box MCP — shell/file ops over stdio). An agent reaches
# it over SSH and can do nothing but talk to agent-box.
#
# Hardening posture (matches the StatefulSet securityContext the daemon sets, so
# the pod satisfies the `restricted` Pod Security profile):
#   - runs as a non-root user (uid 1000)
#   - SSH listens on :2222, an UNPRIVILEGED port — so no capabilities are
#     needed at all (the agent connects to the gateway on :22; :2222 is the
#     internal sshpiper→pod hop).
