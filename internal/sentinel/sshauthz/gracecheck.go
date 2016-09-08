package sshauthz

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// LoginGraceMargin is added to MaxApprovalWait to get the smallest
// sshpiperd --login-grace-time that is safe with device approval: the
// plugin may hold a login for the whole cap and then still has to finish
// the handshake.
const LoginGraceMargin = 10 * time.Second

// MinLoginGrace is the smallest --login-grace-time the check accepts.
const MinLoginGrace = MaxApprovalWait + LoginGraceMargin

// ParentCmdline returns the NUL-separated command line of this process's
// parent (sshpiperd when run as its plugin). It is Linux only; elsewhere or
// when /proc is unreadable it returns an error.
func ParentCmdline() ([]byte, error) {
	return os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", os.Getppid()))
}

// CheckParentLoginGrace logs a WARN through logf when the parent sshpiperd
// was started without a --login-grace-time of at least MinLoginGrace: the
// default 30s would drop a login while the user approves a device. A
