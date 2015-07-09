package server

import "testing"

// TestCoreStaticIP covers the deterministic stable-IP assignment for core
// containers (#240): caddy gets a fixed high host in the bridge subnet so a
// recreate reuses the same IP, keeping the dnsmasq/DNAT references valid.
func TestCoreStaticIP(t *testing.T) {
	tests := []struct {
		name      string
		cidr      string
		container string
		want      string
		wantErr   bool
	}{
		{
			name:      "caddy in /24 gateway form",
			cidr:      "10.100.0.1/24",
			container: CoreCaddyContainer,
			want:      "10.100.0.241",
		},
		{
			name:      "caddy in /24 network form",
			cidr:      "10.20.0.0/24",
			container: CoreCaddyContainer,
			want:      "10.20.0.241",
		},
		{
			name:      "caddy in /16 masks to network base + offset",
			cidr:      "10.100.5.1/16",
			container: CoreCaddyContainer,
			want:      "10.100.0.241",
		},
		{
			// Postgres and the metrics container are pinned too, so the
			// pg_hba.conf written when Postgres is first installed can name the
			// exact address Grafana will have, instead of the whole subnet.
			name:      "postgres is pinned",
			cidr:      "10.100.0.1/24",
			container: CorePostgresContainer,
			want:      "10.100.0.242",
		},
		{
			name:      "metrics container is pinned",
			cidr:      "10.100.0.1/24",
			container: CoreVictoriaMetricsContainer,
			want:      "10.100.0.243",
		},
		{
			name:      "otel collector is pinned",
			cidr:      "10.100.0.1/24",
			container: CoreOTelCollectorContainer,
			want:      "10.100.0.244",
		},
		{
			name:      "security is pinned",
			cidr:      "10.100.0.1/24",
			container: CoreSecurityContainer,
			want:      "10.100.0.245",
		},
		{
			name:      "guacamole is pinned",
			cidr:      "10.100.0.1/24",
			container: CoreGuacamoleContainer,
			want:      "10.100.0.246",
		},
		{
			name:      "container without an assigned offset falls back to DHCP",
			cidr:      "10.100.0.1/24",
