package mcp

import (
	"fmt"
	"strings"

	"github.com/footprintai/containarium/pkg/core/incus"
)

// cpuAdmissionModeFromWire maps the proto enum NAME grpc-gateway emits onto
// the core posture type. This package keeps plain wire structs rather than
// pkg/pb (see the ListBackendsResponse comment in client.go), so the names
// are matched here the same way the passthrough-route protocol names are.
func cpuAdmissionModeFromWire(name string) incus.CPUAdmissionMode {
	switch name {
	case "CPU_ADMISSION_MODE_DISABLED":
