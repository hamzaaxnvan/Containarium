package submit

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// MinGitMajor/MinGitMinor is the minimum host git version
// SubmitTrackerChange requires — decision D2's ">= 2.31", needed for
// GIT_CONFIG_COUNT/GIT_CONFIG_KEY_N/GIT_CONFIG_VALUE_N env-based config
// (GitPusher's hardening mechanism). See
// docs/architecture/agent-tracker-broker.md.
const (
	MinGitMajor = 2
