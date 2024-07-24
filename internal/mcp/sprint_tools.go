package mcp

import (
	"fmt"

	"github.com/footprintai/containarium/internal/client"
)

// sprintTools is the MCP catalog for reading the sprint board a hosted
// control plane mirrors onto each sprint umbrella issue. Both tools are
// read-only and hit routes that exist only on a hosted control plane; against
// a standalone daemon they return that daemon's plain not-found error.
//
// A sprint has no id: it is addressed by its umbrella issue (provider,
// project, number).
func sprintTools() []Tool {
	orgProp := map[string]interface{}{"type": "string", "description": "Organization id."}
