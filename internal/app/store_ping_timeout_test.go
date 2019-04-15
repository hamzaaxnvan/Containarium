package app

import (
	"context"
	"net"
	"testing"
	"time"
)

// NewStore must not block forever on a Postgres that accepts TCP and never
// answers; daemon startup waits on it before it serves (#2824).
