package volume

import (
	"fmt"
	"os/exec"
	"strings"
)

// CLIRunner is the production Runner: it shells out to the host's `incus`
// binary, the same path the rest of the storage code uses. Combined
// stdout+stderr is returned so command failures carry Incus's own message.
