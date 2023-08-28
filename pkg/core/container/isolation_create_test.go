package container

import (
	"strings"
	"testing"
	"time"

	incusapi "github.com/lxc/incus/v7/shared/api"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// vmStagesBackend extends the stages fake with the one extra call the
// non-baked (VM) path makes: installPackages probes cloud-init through
// ExecWithExitCode. Exit 0 = present, so the (shortened) cloud-init wait
// path runs too.
type vmStagesBackend struct {
	*stagesBackend
}
