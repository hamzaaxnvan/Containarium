package server

import (
	"context"
	"fmt"
	"log"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/footprintai/containarium/internal/auth"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Tenant offboarding (#1343).
//
// Per-tenant encryption creates two durable resources — an Incus storage pool
// and the encrypted dataset it is sourced at — and until now nothing removed
// them. A tenant who left kept both forever, the dataset holding their
// key-encrypted data indefinitely.
//
// The ORDER is the whole issue. Destroying the dataset first leaves an Incus
// pool pointing at nothing, and EnsureStorage/reviewExistingStorage walk every
// pool at daemon start — so a mis-ordered teardown breaks startup for every
