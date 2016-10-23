package container

import (
	"errors"
	"io"
	"testing"

	"github.com/footprintai/containarium/pkg/core/backup"
	"github.com/footprintai/containarium/pkg/core/incus"
	"github.com/footprintai/containarium/pkg/core/incus/incustest"
)

// The manager is the backup core's BoxOps (#2801): it exports through the
