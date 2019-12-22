package server

import (
	"fmt"
	"log"
	"strings"

	"github.com/footprintai/containarium/pkg/core/incus"
)

// grafanaIniPath is where the daemon writes Grafana's config inside the
// core-victoriametrics container.
const grafanaIniPath = "/etc/grafana/grafana.ini"

// renderGrafanaIni is the grafana.ini the daemon provisions. Anonymous
// access is off: the dashboard port is on the same bridge as every tenant
// (see docs/security/multi-tenant-isolation.md), so an anonymous Viewer
// role would let any tenant read platform dashboards with no credential at
// all (#2079). Dashboards are reached through Caddy with a login.
func renderGrafanaIni(postgresIP, dbUser, dbPassword, adminPassword string) string {
	// An empty adminPassword omits the key: Grafana then uses its own default
	// login and prompts for a change at first login, rather than this template
	// shipping a literal one (#2091).
	adminLine := ""
	if adminPassword != "" {
		adminLine = "admin_password = " + grafanaIniValue(adminPassword) + "\n"
	}
	return fmt.Sprintf(`[database]
type = postgres
host = %s:5432
name = grafana
user = %s
password = %s
ssl_mode = disable
max_open_conn = 5
max_idle_conn = 2
conn_max_lifetime = 14400

[security]
allow_embedding = true
admin_user = admin
%s
[auth.anonymous]
enabled = false

[users]
default_theme = light

[server]
http_port = 3000
root_url = %%(protocol)s://%%(domain)s/grafana/
serve_from_sub_path = true
`, postgresIP, dbUser, grafanaIniValue(dbPassword), adminLine)
}

// grafanaAnonymousEnabled reports the value of [auth.anonymous] enabled in
// an ini text. ok is false when the section or key is absent — Grafana's
// own default is then "disabled", so absent is safe.
func grafanaAnonymousEnabled(ini string) (enabled, ok bool) {
	inSection := false
	for _, raw := range strings.Split(ini, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "[") {
			inSection = strings.EqualFold(line, "[auth.anonymous]")
			continue
		}
		if !inSection {
			continue
		}
		key, val, found := strings.Cut(line, "=")
		if !found || !strings.EqualFold(strings.TrimSpace(key), "enabled") {
			continue
		}
		return strings.EqualFold(strings.TrimSpace(val), "true"), true
	}
	return false, false
}

// disableGrafanaAnonymous rewrites [auth.anonymous] enabled to false and
// reports whether anything changed. Only that one key is touched; a text
// with anonymous already off, or with no such section, is returned as is.
func disableGrafanaAnonymous(ini string) (string, bool) {
	if enabled, ok := grafanaAnonymousEnabled(ini); !ok || !enabled {
		return ini, false
	}
	lines := strings.Split(ini, "\n")
	inSection := false
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "[") {
			inSection = strings.EqualFold(line, "[auth.anonymous]")
			continue
		}
