package sentinel

import (
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

// userspaceForwarder is a TCP proxy that runs on the sentinel host,
// listening on a small set of ports and forwarding each accepted
// connection to a fixed backend with an optional PROXY v2 header
// prepended. It exists for the "simple proxy" sentinel mode (single
// GCP spot VM behind the sentinel) — in that mode the legacy code
// path uses kernel iptables DNAT, which cannot inject a PROXY v2
// header because there is no userspace process in the hot path. With
// PROXY v2 missing, the downstream Caddy sees the connection peer as
// the sentinel-NAT'd address (a bridge gateway or sentinel IP, not
// the real client), so visitor-IP-dependent features (logs, audit,
// XFF-aware app code) all see the wrong IP.
//
// This forwarder is only activated when --proxy-protocol is set AND
// the sentinel is NOT already running the multiplexed ConnMux path
// (the multi-pool / tunnel-promoted-primary mode, which has its own
