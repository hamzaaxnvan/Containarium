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
