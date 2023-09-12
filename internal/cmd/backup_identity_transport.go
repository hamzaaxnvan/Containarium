package cmd

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// requireSecureTransportForIdentity refuses to send an age identity — a
// private key — over a cleartext transport to a non-loopback host.
