package server

import (
	"os"
	"strings"
)

// envBool reads the named env var and returns true when
// it's set to a recognized truthy value (case-insensitive
