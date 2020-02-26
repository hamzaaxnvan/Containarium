package container

import (
	"fmt"
	"io"
)

// Whole-box backup (#2801): the container layer's half of backup.BoxOps.

// ExportInstance streams the instance as an Incus backup tarball through
