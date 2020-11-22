# containarium-mcp-server
#
# The platform MCP server (cmd/mcp-server): the outside-the-box admin
# surface (create_container, list_containers, expose_port, ...). Reached
# over stdio (JSON-RPC), speaking HTTP + JWT Bearer to the Containarium
# REST API — see cmd/mcp-server/README.md for the full contract.
#
# This image exists so the server can be built, deployed, and scored
# (glama.ai) as a standalone MCP server (Containarium#967). It is a pure-Go
# REST/gRPC client with no host-side dependencies (unlike cmd/agent-box,
# which must run inside a Containarium box).
#
