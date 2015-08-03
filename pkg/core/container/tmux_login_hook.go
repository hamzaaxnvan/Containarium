package container

import (
	_ "embed"
	"log"

	"github.com/footprintai/containarium/pkg/core/ospkg"
)

// tmuxLoginHookPath is where the box's persistent-login hook lives (#2121).
// /etc/profile sources /etc/profile.d/*.sh for login shells on both Debian/
// Ubuntu and RHEL/Rocky, and every box user is created with /bin/bash.
const tmuxLoginHookPath = "/etc/profile.d/containarium-tmux.sh"

// tmuxLoginHook is the hook itself. The guards live in the script (and are
// documented there); scripts/test-box-tmux-login-hook.sh runs this exact file
// under a real shell on a real pty.
//
//go:embed tmux_login_hook.sh
var tmuxLoginHook []byte

// installLoginExtras gives the box what a phone SSH client needs (#2121):
// mosh-server (where the distro needs more than BasePackages to get it) and
// the tmux-on-interactive-login hook. It runs inside installPackages, so a
// baked base image carries it too. Best-effort by design: a box without mosh
// or without the hook is still a working box, so neither failure fails the
// create. It never touches sshd config or authorized_keys.
