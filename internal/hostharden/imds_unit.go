package hostharden

import (
	"fmt"
	"os"
	"strings"
)

// ImdsBlockUnitPath is the systemd unit BlockMetadataFromBridge's rule
// needs to survive a reboot — `iptables` rules are runtime kernel state,
// not persisted by the package itself, and this repo doesn't assume any
