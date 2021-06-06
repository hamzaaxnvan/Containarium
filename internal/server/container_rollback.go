package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/auth"
	"github.com/footprintai/containarium/pkg/core/box"
	"github.com/footprintai/containarium/pkg/core/zfscrypt"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Container snapshot rollback (#1160b).
//
// `zfscrypt.RollbackToSnapshot` has existed since #1200 and is verified
// against a real pool. What this adds is the three refusals that make a
// destructive operation safe to expose over an API — and each of them is
// checked BEFORE anything is destroyed, because a guard that reports an
// error after rolling back is not a guard.
//
//  1. A running container. ZFS would refuse the busy dataset anyway, but
//     "dataset is busy" sends an operator looking for a mount problem
//     instead of telling them the one thing to do. With force, the daemon
//     stops it first and leaves it stopped.
//  2. Newer snapshots. Rolling back past them destroys them, which widens
//     the blast radius from "lose writes since the snapshot" to "lose other
//     restore points too". Opt-in, and the response names what went.
//  3. An unavailable encryption key. A rollback whose result cannot be read
