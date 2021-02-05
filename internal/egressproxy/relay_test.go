package egressproxy

import (
	"context"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"
)

func TestParseAllow_AndSourceAllowed(t *testing.T) {
	r, err := New("127.0.0.1:0", "127.0.0.1:1", []string{"10.100.0.156", "192.168.0.0/16"}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cases := []struct {
		ip   string
		want bool
	}{
		{"10.100.0.156", true},  // exact box IP (/32)
		{"10.100.0.157", false}, // sibling box on the same bridge — must be denied
		{"192.168.5.9", true},   // inside the CIDR
		{"10.0.0.1", false},     // unrelated
	}
	for _, c := range cases {
		got := r.sourceAllowed(mustAddr(t, c.ip))
		if got != c.want {
			t.Errorf("sourceAllowed(%s) = %v, want %v", c.ip, got, c.want)
		}
	}
}
