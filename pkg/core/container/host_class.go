package container

import (
	"os/exec"
	"strings"
	"sync"
)

// Host-class detection for environment-specific code paths in
// jump_server.go and friends.
//
// Background: the useradd preconditions (`systemctl stop google-guest-
// agent`, force-removing `/etc/.pwd.lock`) were written for GCP VMs,
// where `google-guest-agent` races with local `useradd` over the
// passwd lock. On any non-GCP host (a VirtualBox lab spot, an on-prem
// box, AWS, Azure, …) those steps are at best no-ops with misleading
