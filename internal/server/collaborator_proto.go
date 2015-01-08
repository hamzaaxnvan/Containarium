package server

import (
	"strings"

	"github.com/footprintai/containarium/internal/collaborator"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// collaboratorToProto maps a stored collaborator row onto its wire type.
