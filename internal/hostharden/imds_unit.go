package hostharden

import (
	"fmt"
	"os"
	"strings"
)

// ImdsBlockUnitPath is the systemd unit BlockMetadataFromBridge's rule
// needs to survive a reboot — `iptables` rules are runtime kernel state,
// not persisted by the package itself, and this repo doesn't assume any
// particular distro's persistence mechanism (iptables-persistent,
// netfilter-persistent, firewalld) is installed. A oneshot unit that
// re-runs the same idempotent check-then-insert this package already does
// is simpler than depending on one of those and works everywhere systemd
// does.
const ImdsBlockUnitPath = "/etc/systemd/system/containarium-imds-block.service"

// InstallPersistentUnit writes and enables a systemd oneshot unit that
// re-applies BlockMetadataFromBridge's rule on every boot. Idempotent:
// re-running (e.g. on a re-enroll) overwrites the same content and
// `systemctl enable` on an already-enabled unit is a no-op.
