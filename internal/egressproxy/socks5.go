package egressproxy

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"
)

// ServeSOCKS5 runs a minimal no-auth SOCKS5 CONNECT proxy on listenAddr until
// ctx is cancelled. It egresses from whatever host runs it — used by the
// operator side of "egress via client" (#808): the box's traffic, tunnelled to
// here, leaves with this machine's IP. Returns the bound address (useful when
// listenAddr used port 0).
//
// Scope is intentionally small: no auth, CONNECT only, IPv4 + domain targets —
// enough for an HTTP(S) browser. It is meant to be bound to loopback and
// exposed to the box exclusively through the authenticated ssh -R tunnel.
func ServeSOCKS5(ctx context.Context, listenAddr string, logf func(string, ...any)) (string, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return "", fmt.Errorf("socks listen %s: %w", listenAddr, err)
	}
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
