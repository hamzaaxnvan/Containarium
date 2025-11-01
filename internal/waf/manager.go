package waf

import (
	"context"
	"fmt"
	"log"
	"net"
)

// Config configures the steering proxy. Addr is required; the rest is the PR-2
// inspection layer (nil Inspector → PR-1 forward-only behavior).
type Config struct {
	Addr         string
	Inspector    Inspector                                  // nil → forward-only
