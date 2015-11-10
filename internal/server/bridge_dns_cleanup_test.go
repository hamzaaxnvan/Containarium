package server

import (
	"errors"
	"testing"
)

// #2734 (slice C of #2263): on upgrade the daemon strips the bridge DNS record
// earlier daemons wrote and leaves every other raw.dnsmasq line alone
// (decision Q36 on #2727).
func TestStripBridgeDNSRecord(t *testing.T) {
	const old = "address=/example.com/10.0.3.5\nserver=/ssh.example.com/#\nserver=/api.example.com/#"
	cases := []struct {
		name, raw, base, ssh string
		want                 string
		changed              bool
	}{
		{name: "no base domain (BYOC) leaves the value alone", raw: old, base: "", ssh: "ssh.example.com", want: old},
		{name: "invalid base domain leaves the value alone", raw: old, base: "exa mple.com", want: old},
		{name: "empty value", raw: "", base: "example.com", want: ""},
		{
			name: "no address line: carve-out-shaped lines are kept",
			raw:  "server=/ssh.example.com/#\nserver=/api.example.com/#",
			base: "example.com", ssh: "ssh.example.com",
			want: "server=/ssh.example.com/#\nserver=/api.example.com/#",
		},
		{name: "the whole daemon record goes and the key is unset", raw: old, base: "example.com", ssh: "ssh.example.com", want: "", changed: true},
		{
			name: "only blank lines left over: the key is unset",
			raw:  "address=/example.com/10.0.3.5\n\nserver=/ssh.example.com/#\n",
			base: "example.com", ssh: "ssh.example.com",
			want: "", changed: true,
		},
		{
			name: "operator lines survive byte for byte, in order",
			raw:  "# operator\ndhcp-option=6,10.0.3.1\naddress=/example.com/10.0.3.5\nserver=/ssh.example.com/#\naddress=/other.test/10.0.3.9\n  server=/corp.internal/10.1.1.1  \nserver=/unrelated.test/#",
			base: "example.com", ssh: "ssh.example.com",
			want:    "# operator\ndhcp-option=6,10.0.3.1\naddress=/other.test/10.0.3.9\n  server=/corp.internal/10.1.1.1  \nserver=/unrelated.test/#",
			changed: true,
		},
		{
			name: "ssh host outside the base domain is still the daemon's carve-out",
