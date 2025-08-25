package server

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// dnsTTLResolver is the TTL-aware lookup path (#2379): net.Resolver exposes no
// record TTLs, so this sends A and AAAA queries itself (UDP, EDNS0) to the
// system's configured nameservers and reports the smallest answer TTL.
// Names are queried as absolute (no search-list expansion): egress_domains are
// FQDNs. A truncated reply is an error, so the caller falls back to the stdlib
// resolver (which retries over TCP) with the TTL unknown.
type dnsTTLResolver struct {
	servers []string // host:port, tried in order
	timeout time.Duration
}

// fallbackTTLResolver tries primary, then fallback on any error.
type fallbackTTLResolver struct {
	primary, fallback addrTTLResolver
}

func (f fallbackTTLResolver) LookupAddrsTTL(ctx context.Context, host string) (dnsAnswer, error) {
	if ans, err := f.primary.LookupAddrsTTL(ctx, host); err == nil {
		return ans, nil
	}
	return f.fallback.LookupAddrsTTL(ctx, host)
}

// newSystemTTLResolver is the production resolver: raw DNS against
// /etc/resolv.conf's nameservers, falling back to net.DefaultResolver (TTL
// unknown → the 60s cap, today's behaviour) when that path fails or no
// nameserver is configured.
func newSystemTTLResolver() addrTTLResolver {
	std := stdlibTTLResolver{r: net.DefaultResolver}
	servers := resolvConfServers("/etc/resolv.conf")
	if len(servers) == 0 {
		return std
	}
	return fallbackTTLResolver{primary: &dnsTTLResolver{servers: servers, timeout: 5 * time.Second}, fallback: std}
}

// resolvConfServers returns the "nameserver" entries of a resolv.conf as
