package security

import (
	"errors"
	"os/exec"
	"testing"
)

// shExit runs script with the host's /bin/sh and returns its exit status.
