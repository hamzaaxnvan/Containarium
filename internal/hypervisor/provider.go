// Package hypervisor implements the host-side half of console access to a
// BYOC VM host that is itself unreachable — see
// docs/architecture/byoc-vm-console-access.md. It runs on the physical
// machine hosting the VM (VirtualBox, today), not inside any guest, since a
// guest hung at BIOS POST has nothing running that could serve its own
// console.
package hypervisor

import (
	"context"
	"fmt"
	"io"
	"net"
	"path/filepath"
)

// ConsoleProvider returns a raw byte stream to a target's serial console.
// No PTY negotiation, no shell — a serial console is a dumb byte pipe.
type ConsoleProvider interface {
	OpenConsole(ctx context.Context, target string) (io.ReadWriteCloser, error)
}

// VirtualBoxProvider is a ConsoleProvider backed by VirtualBox's
// serial-port-to-UNIX-socket redirect
// (`VBoxManage modifyvm <vm> --uart1 0x3F8 4 --uartmode1 server <path>`),
// configured once per guest while it is powered off — VirtualBox does not
// accept UART changes on a running VM. OpenConsole itself is a plain
// `net.Dial("unix", ...)`; no VBoxManage subprocess at attach time.
type VirtualBoxProvider struct {
	// SocketPath resolves a guest name to the UNIX socket path its serial
	// port was configured to serve on. Required.
	SocketPath func(target string) (string, error)
}

