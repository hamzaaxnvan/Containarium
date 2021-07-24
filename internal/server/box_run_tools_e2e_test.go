//go:build !containarium_client

package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// End to end on Linux (the run wrapper needs setsid): a fake engine installs a tool during the run;
// the run's own wrapper records it, the daemon lists it as ADDED_DURING_RUN, and a tool that was
// already there is PRESENT_AT_START. No model is involved.
