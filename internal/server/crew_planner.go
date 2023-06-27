package server

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/pkg/core/taskgraph"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
)

const plannerSkillID = "task-planner"

type plannerAssignee struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}

type plannerInput struct {
