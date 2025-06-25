package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/auth"
)

// The gate's staging scope is the AUTHENTICATED SUBJECT, not the deploy
// name (#2414). An admin may deploy a box under any name, so a name-derived
// scope would let an admin-token proxy that forwards a user-chosen name read
// any tenant's staged dataset. These tests pin the rule.

// moveStaged moves the fixture's staged dataset (staged under alice) to the
// same ref under owner, so only owner's staging directory holds it.
