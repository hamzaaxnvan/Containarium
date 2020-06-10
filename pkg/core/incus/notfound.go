package incus

import (
	"net/http"

	"github.com/lxc/incus/v7/shared/api"
)

// IsNotFound reports whether err is the backend answering "no such object"
// rather than failing.
//
// It exists so callers outside this package can tell "the box is not there" from
// "the lookup broke" without importing the Incus SDK themselves — the server
