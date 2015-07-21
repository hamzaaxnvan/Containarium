package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// An age identity is a private key. It must never cross a cleartext
// transport to a non-loopback host (review feedback on #2302, CWE-319).
func TestRequireSecureTransportForIdentity(t *testing.T) {
	tests := []struct {
		name     string
		server   string
		httpMode bool
		insecure bool
		wantErr  bool
	}{
		{name: "no server configured is left to the client", server: ""},
		{name: "gRPC with mTLS", server: "host.example.com:50051"},
		{name: "gRPC --insecure to a remote host", server: "host.example.com:50051", insecure: true, wantErr: true},
		{name: "gRPC --insecure to loopback", server: "127.0.0.1:50051", insecure: true},
		{name: "http mode, https URL", server: "https://host.example.com", httpMode: true},
		{name: "http mode, explicit http URL", server: "http://host.example.com", httpMode: true, wantErr: true},
		{name: "http mode, scheme-less (client prepends http://)", server: "host.example.com:8080", httpMode: true, wantErr: true},
		{name: "http mode, http to localhost", server: "http://localhost:8080", httpMode: true},
		{name: "http mode, scheme-less loopback", server: "127.0.0.1:8080", httpMode: true},
		{name: "http mode, http to IPv6 loopback", server: "http://[::1]:8080", httpMode: true},
		{name: "http mode, http to a private LAN IP is still cleartext", server: "http://10.0.0.5:8080", httpMode: true, wantErr: true},
		{name: "http mode, lookalike host is not loopback", server: "http://localhost.evil.example:8080", httpMode: true, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := requireSecureTransportForIdentity(tc.server, tc.httpMode, tc.insecure)
			if (err != nil) != tc.wantErr {
				t.Fatalf("requireSecureTransportForIdentity(%q, http=%t, insecure=%t) error = %v, wantErr %t",
					tc.server, tc.httpMode, tc.insecure, err, tc.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), "age identity") {
				t.Errorf("error should name the age identity so the operator knows why: %v", err)
			}
		})
