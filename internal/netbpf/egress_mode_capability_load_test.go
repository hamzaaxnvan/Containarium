//go:build ebpf_load

package netbpf

import (
	"os"
	"testing"
)

// envPreEgressModeObject names a netpolicy.bpf.o compiled from the source just
