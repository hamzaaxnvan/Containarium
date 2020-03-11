package mcp

import (
	"fmt"

	"github.com/footprintai/containarium/internal/client"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// SSH session control (#2522): terminate_user_sessions wraps the same client
// call `containarium sessions terminate` makes.

func sessionTools() []Tool {
	return []Tool{{
		Name: "terminate_user_sessions",
		Description: "End every live SSH session a user holds on the backend: their own box and " +
			"every box they reach as a collaborator, in the box and on the host jump account. " +
