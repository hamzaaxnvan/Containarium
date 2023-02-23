package hostport

import "testing"

func TestSplit(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		defPort  int
		wantHost string
		wantPort int
	}{
		// Ported from internal/sshconfig/generate_test.go's former
		// TestSplitHostPort — bare host / host:port / bracketed IPv6,
		// with and without an explicit port.
		{"bare host uses default port", "sentinel.example.com", 22, "sentinel.example.com", 22},
		{"host:port", "sentinel.example.com:2222", 22, "sentinel.example.com", 2222},
		{"bracketed IPv6 with port", "[2001:db8::1]:2222", 22, "2001:db8::1", 2222},
		{"bracketed IPv6 without port uses default", "[2001:db8::1]", 22, "2001:db8::1", 22},

